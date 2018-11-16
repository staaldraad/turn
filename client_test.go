package turn

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/gortc/stun"
	"github.com/gortc/turn/testutil"
)

type testSTUN struct {
	indicate func(m *stun.Message) error
	do       func(m *stun.Message, f func(e stun.Event)) error
}

func (t testSTUN) Indicate(m *stun.Message) error { return t.indicate(m) }

func (t testSTUN) Do(m *stun.Message, f func(e stun.Event)) error { return t.do(m, f) }

func TestNewClient(t *testing.T) {
	t.Run("NoConn", func(t *testing.T) {
		c, createErr := NewClient(ClientOptions{})
		if createErr == nil {
			t.Error("should error")
		}
		if c != nil {
			t.Error("client should be nil")
		}
	})
	t.Run("Simple", func(t *testing.T) {
		connL, connR := net.Pipe()
		c, createErr := NewClient(ClientOptions{
			Conn: connR,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if c == nil {
			t.Fatal("client should not be nil")
		}
		connL.Close()
	})
	t.Run("RefreshRate", func(t *testing.T) {
		t.Run("Default", func(t *testing.T) {
			connL, connR := net.Pipe()
			c, createErr := NewClient(ClientOptions{
				Conn: connR,
			})
			if createErr != nil {
				t.Fatal(createErr)
			}
			if c == nil {
				t.Fatal("client should not be nil")
			}
			if c.RefreshRate() != defaultRefreshRate {
				t.Error("refresh rate not equals to default")
			}
			connL.Close()
		})
		t.Run("Disabled", func(t *testing.T) {
			connL, connR := net.Pipe()
			c, createErr := NewClient(ClientOptions{
				RefreshDisabled: true,
				Conn:            connR,
			})
			if createErr != nil {
				t.Fatal(createErr)
			}
			if c == nil {
				t.Fatal("client should not be nil")
			}
			if c.RefreshRate() != 0 {
				t.Error("refresh rate not equals to zero")
			}
			connL.Close()
		})
		t.Run("Custom", func(t *testing.T) {
			connL, connR := net.Pipe()
			c, createErr := NewClient(ClientOptions{
				RefreshRate: time.Second,
				Conn:        connR,
			})
			if createErr != nil {
				t.Fatal(createErr)
			}
			if c == nil {
				t.Fatal("client should not be nil")
			}
			if c.RefreshRate() != time.Second {
				t.Error("refresh rate not equals to value")
			}
			connL.Close()
		})
	})
}

type verboseConn struct {
	name string
	conn net.Conn
	t    *testing.T
}

func verboseBytes(b []byte) string {
	switch {
	case stun.IsMessage(b):
		m := stun.New()
		if _, err := m.Write(b); err != nil {
			return "stun (invalid)"
		}
		return fmt.Sprintf("stun (%s)", m)
	case IsChannelData(b):
		d := ChannelData{
			Raw: b,
		}
		if err := d.Decode(); err != nil {
			return "chandata (invalid)"
		}
		return fmt.Sprintf("chandata (n: 0x%x, len: %d)", int(d.Number), d.Length)
	default:
		return "raw"
	}
}

func (c *verboseConn) Read(b []byte) (n int, err error) {
	c.t.Helper()
	c.t.Logf("%s: read start", c.name)
	defer func() {
		c.t.Helper()
		if err != nil {
			c.t.Logf("%s: read: %v", c.name, err)
		} else {
			c.t.Logf("%s: read(%d): %s", c.name, n, verboseBytes(b))
		}
	}()
	return c.conn.Read(b)
}

func (c *verboseConn) Write(b []byte) (n int, err error) {
	c.t.Helper()
	c.t.Logf("%s: write start: %s", c.name, verboseBytes(b))
	defer func() {
		c.t.Helper()
		if err != nil {
			c.t.Logf("%s: write: %s: %v", c.name, verboseBytes(b), err)
		} else {
			c.t.Logf("%s: write: %s", c.name, verboseBytes(b))
		}
	}()
	return c.conn.Write(b)
}

func (c *verboseConn) Close() error {
	c.t.Helper()
	c.t.Logf("%s: close", c.name)
	return c.conn.Close()
}

func (c *verboseConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *verboseConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *verboseConn) SetDeadline(t time.Time) error {
	c.t.Helper()
	c.t.Logf("%s: SetDeadline(%s)", c.name, t)
	return c.conn.SetDeadline(t)
}

func (c *verboseConn) SetReadDeadline(t time.Time) error {
	c.t.Helper()
	c.t.Logf("%s: SetReadDeadline(%s)", c.name, t)
	return c.conn.SetReadDeadline(t)
}

func (c *verboseConn) SetWriteDeadline(t time.Time) error {
	c.t.Helper()
	c.t.Logf("%s: SetWriteDeadline(%s)", c.name, t)
	return c.conn.SetWriteDeadline(t)
}

func testPipe(t *testing.T, lName, rName string) (net.Conn, net.Conn) {
	connL, connR := net.Pipe()
	return &verboseConn{
			name: lName,
			conn: connL,
			t:    t,
		}, &verboseConn{
			name: rName,
			conn: connR,
			t:    t,
		}
}

func TestClientMultiplexed(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	connL, connR := testPipe(t, "server", "client")
	timeout := time.Second * 10
	c, createErr := NewClient(ClientOptions{
		Log:          logger,
		Conn:         connR,
		RTO:          timeout,
		NoRetransmit: true,
	})
	if createErr != nil {
		t.Fatal(createErr)
	}
	if c == nil {
		t.Fatal("client should not be nil")
	}
	gotRequest := make(chan struct{})
	connL.SetDeadline(time.Now().Add(timeout))
	connR.SetDeadline(time.Now().Add(timeout))
	go func() {
		buf := make([]byte, 1500)
		readN, readErr := connL.Read(buf)
		t.Log("got write")
		if readErr != nil {
			t.Error("failed to read")
		}
		m := &stun.Message{
			Raw: buf[:readN],
		}
		if decodeErr := m.Decode(); decodeErr != nil {
			t.Error("failed to decode")
		}
		res := stun.MustBuild(m, stun.NewType(m.Type.Method, stun.ClassSuccessResponse),
			&RelayedAddress{
				IP:   net.IPv4(127, 0, 0, 1),
				Port: 1001,
			},
			&stun.XORMappedAddress{
				IP:   net.IPv4(127, 0, 0, 2),
				Port: 1002,
			},
			stun.Fingerprint,
		)
		res.Encode()
		if _, writeErr := connL.Write(res.Raw); writeErr != nil {
			t.Error("failed to write")
		}
		gotRequest <- struct{}{}
	}()
	a, allocErr := c.Allocate()
	if allocErr != nil {
		t.Fatal(allocErr)
	}
	select {
	case <-gotRequest:
		// success
	case <-time.After(timeout):
		t.Fatal("timed out")
	}
	peer := &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 3),
		Port: 1003,
	}
	go func() {
		buf := make([]byte, 1500)
		readN, readErr := connL.Read(buf)
		if readErr != nil {
			t.Error("failed to read")
		}
		m := &stun.Message{
			Raw: buf[:readN],
		}
		if decodeErr := m.Decode(); decodeErr != nil {
			t.Error("failed to decode")
		}
		t.Logf("read request: %s", m)
		res := stun.MustBuild(m, stun.NewType(m.Type.Method, stun.ClassSuccessResponse),
			stun.Fingerprint,
		)
		t.Logf("writing response: %s", res)
		if _, writeErr := connL.Write(res.Raw); writeErr != nil {
			t.Error("failed to write")
		}
		t.Logf("wrote %s", res)
		gotRequest <- struct{}{}
	}()
	t.Log("creating udp permission")
	p, permErr := a.CreateUDP(peer)
	if permErr != nil {
		t.Fatal(permErr)
	}
	select {
	case <-gotRequest:
		// success
	case <-time.After(timeout):
		t.Fatal("timed out")
	}
	if p.Bound() {
		t.Error("should not be bound")
	}
	go func() {
		buf := make([]byte, 1500)
		readN, readErr := connL.Read(buf)
		t.Log("got write")
		if readErr != nil {
			t.Error("failed to read")
		}
		m := &stun.Message{
			Raw: buf[:readN],
		}
		if decodeErr := m.Decode(); decodeErr != nil {
			t.Error("failed to decode")
		}
		res := stun.MustBuild(m, stun.NewType(m.Type.Method, stun.ClassSuccessResponse),
			stun.Fingerprint,
		)
		res.Encode()
		connL.SetWriteDeadline(time.Now().Add(timeout / 2))
		if _, writeErr := connL.Write(res.Raw); writeErr != nil {
			t.Error("failed to write")
		}
		gotRequest <- struct{}{}
	}()
	t.Log("starting binding")
	if bindErr := p.Bind(); bindErr != nil {
		t.Fatalf("failed to bind: %v", bindErr)
	}
	select {
	case <-gotRequest:
		// success
	case <-time.After(timeout):
		t.Fatal("timed out")
	}
	go func() {
		buf := make([]byte, 1500)
		readN, readErr := connL.Read(buf)
		t.Log("got write")
		if readErr != nil {
			t.Error("failed to read")
		}
		m := &ChannelData{
			Raw: buf[:readN],
		}
		if decodeErr := m.Decode(); decodeErr != nil {
			t.Error("failed to decode")
		}
		gotRequest <- struct{}{}
	}()
	sent := []byte{1, 2, 3, 4}
	if _, writeErr := p.Write(sent); writeErr != nil {
		t.Fatal(writeErr)
	}
	select {
	case <-gotRequest:
		// success
	case <-time.After(timeout):
		t.Fatal("timed out")
	}
	go func() {
		d := &ChannelData{
			Number: p.Binding(),
			Data:   sent,
			Raw:    make([]byte, 1500),
		}
		d.Encode()
		_, writeErr := connL.Write(d.Raw)
		if writeErr != nil {
			t.Error("failed to write")
		}
	}()
	buf := make([]byte, 1500)
	n, readErr := p.Read(buf)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(buf[:n], sent) {
		t.Error("data mismatch")
	}
	connL.Close()
	testutil.EnsureNoErrors(t, logs)
}

func TestClient_Allocate(t *testing.T) {
	t.Run("Anonymous", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		logger := zap.New(core)
		connL, connR := net.Pipe()
		stunClient := &testSTUN{}
		c, createErr := NewClient(ClientOptions{
			Log:  logger,
			Conn: connR, // should not be used
			STUN: stunClient,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		stunClient.indicate = func(m *stun.Message) error {
			t.Fatal("should not be called")
			return nil
		}
		t.Run("Error", func(t *testing.T) {
			doErr := errors.New("error")
			stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
				return doErr
			}
			if _, allocErr := c.Allocate(); allocErr != doErr {
				t.Fatal("unexpected error")
			}
		})
		t.Run("PartialResponse", func(t *testing.T) {
			for _, tc := range []struct {
				Name    string
				Message func(message *stun.Message) *stun.Message
			}{
				{
					Name: "RelayedAddr",
					Message: func(m *stun.Message) *stun.Message {
						return stun.MustBuild(m, stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse),
							stun.Fingerprint,
						)
					},
				},
				{
					Name: "XORMappedAddr",
					Message: func(m *stun.Message) *stun.Message {
						return stun.MustBuild(m, stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse),
							&stun.RawAttribute{
								Type:  stun.AttrXORMappedAddress,
								Value: []byte{1, 2, 3},
							},
							stun.Fingerprint,
						)
					},
				},
			} {
				t.Run(tc.Name, func(t *testing.T) {
					do := func(m *stun.Message, f func(stun.Event)) error {
						f(stun.Event{
							Message: tc.Message(m),
						})
						return nil
					}
					stunClient.do = do
					if _, allocErr := c.Allocate(); allocErr == nil {
						t.Error("expected error")
					}
				})
			}
		})
		stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
			if m.Type != AllocateRequest {
				t.Errorf("bad request type: %s", m.Type)
			}
			f(stun.Event{
				Message: stun.MustBuild(m, stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse),
					&RelayedAddress{
						Port: 1113,
						IP:   net.IPv4(127, 0, 0, 2),
					},
					stun.Fingerprint,
				),
			})
			return nil
		}
		a, allocErr := c.Allocate()
		if allocErr != nil {
			t.Fatal(allocErr)
		}
		peer := &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 1001,
		}
		t.Run("CreateError", func(t *testing.T) {
			addr := &net.UDPAddr{
				IP:   net.IPv4(127, 0, 0, 1),
				Port: 1003,
			}
			t.Run("Do", func(t *testing.T) {
				doErr := errors.New("error")
				stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
					return doErr
				}
				if _, permAddr := a.Create(addr); permAddr != doErr {
					t.Errorf("unexpected err: %v", permAddr)
				}
			})
			t.Run("ErrorCode", func(t *testing.T) {
				stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
					f(stun.Event{
						Message: stun.MustBuild(m, stun.NewType(m.Type.Method, stun.ClassErrorResponse),
							stun.CodeBadRequest,
							stun.Fingerprint,
						),
					})
					return nil
				}
				if _, permAddr := a.Create(addr); permAddr == nil {
					t.Errorf("error expected")
				}
				t.Run("NoCode", func(t *testing.T) {
					stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
						f(stun.Event{
							Message: stun.MustBuild(m, stun.NewType(m.Type.Method, stun.ClassErrorResponse),
								stun.Fingerprint,
							),
						})
						return nil
					}
					if _, permAddr := a.Create(addr); permAddr == nil {
						t.Errorf("error expected")
					}
				})
			})
		})
		stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
			if m.Type != stun.NewType(stun.MethodCreatePermission, stun.ClassRequest) {
				t.Errorf("bad request type: %s", m.Type)
			}
			f(stun.Event{
				Message: stun.MustBuild(m, stun.NewType(m.Type.Method, stun.ClassSuccessResponse),
					stun.Fingerprint,
				),
			})
			return nil
		}
		t.Run("Create", func(t *testing.T) {
			t.Run("UDP", func(t *testing.T) {
				if _, permAddr := a.Create(&net.UDPAddr{
					IP:   net.IPv4(127, 0, 0, 1),
					Port: 1002,
				}); permAddr != nil {
					t.Error(permAddr)
				}
			})
			t.Run("Unexpected", func(t *testing.T) {
				if _, permAddr := a.Create(&net.IPAddr{
					IP: net.IPv4(127, 0, 0, 1),
				}); permAddr == nil {
					t.Error("error expected")
				}
			})
		})
		p, permErr := a.CreateUDP(peer)
		if permErr != nil {
			t.Fatal(allocErr)
		}
		stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
			t.Fatal("should not be called")
			return nil
		}
		stunClient.indicate = func(m *stun.Message) error {
			if m.Type != stun.NewType(stun.MethodSend, stun.ClassIndication) {
				t.Errorf("bad request type: %s", m.Type)
			}
			var (
				data     Data
				peerAddr PeerAddress
			)
			if err := m.Parse(&data, &peerAddr); err != nil {
				return err
			}
			go c.stunHandler(stun.Event{
				Message: stun.MustBuild(stun.TransactionID,
					stun.NewType(stun.MethodData, stun.ClassIndication),
					data, peerAddr,
					stun.Fingerprint,
				),
			})
			return nil
		}
		sent := []byte{1, 2, 3, 4}
		if _, writeErr := p.Write(sent); writeErr != nil {
			t.Fatal(writeErr)
		}
		buf := make([]byte, 1500)
		n, readErr := p.Read(buf)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(buf[:n], sent) {
			t.Error("data mismatch")
		}
		testutil.EnsureNoErrors(t, logs)
		t.Run("Binding", func(t *testing.T) {
			var (
				n        ChannelNumber
				bindPeer PeerAddress
			)
			stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
				if m.Type != stun.NewType(stun.MethodChannelBind, stun.ClassRequest) {
					t.Errorf("unexpected type %s", m.Type)
				}
				if parseErr := m.Parse(&n, &bindPeer); parseErr != nil {
					t.Error(parseErr)
				}
				if !Addr(bindPeer).Equal(Addr{
					Port: peer.Port,
					IP:   peer.IP,
				}) {
					t.Errorf("unexpected bind peer %s", bindPeer)
				}
				f(stun.Event{
					Message: stun.MustBuild(m,
						stun.NewType(m.Type.Method, stun.ClassSuccessResponse),
					),
				})
				return nil
			}
			if bErr := p.Bind(); bErr != nil {
				t.Error(bErr)
			}
			if !p.Bound() {
				t.Error("should be bound")
			}
			if p.Binding() != n {
				t.Error("invalid channel number")
			}
			if bErr := p.Bind(); bErr != ErrAlreadyBound {
				t.Error("should be already bound")
			}
			sent := []byte{1, 2, 3, 4}
			gotWrite := make(chan struct{})
			timeout := time.Second * 5
			go func() {
				buf := make([]byte, 1500)
				connL.SetReadDeadline(time.Now().Add(timeout))
				readN, readErr := connL.Read(buf)
				if readErr != nil {
					t.Error("failed to read")
				}
				d := ChannelData{
					Raw: buf[:readN],
				}
				if decodeErr := d.Decode(); decodeErr != nil {
					t.Errorf("failed to decode channel data: %v", decodeErr)
				}
				if !bytes.Equal(d.Data, sent) {
					t.Error("decoded channel data payload is invalid")
				}
				if d.Number != n {
					t.Error("decoded channel number is invalid")
				}
				gotWrite <- struct{}{}
			}()
			if _, writeErr := p.Write(sent); writeErr != nil {
				t.Fatal(writeErr)
			}
			select {
			case <-gotWrite:
				// success
			case <-time.After(timeout):
				t.Fatal("timed out")
			}
			go func() {
				d := ChannelData{
					Data:   sent,
					Number: n,
				}
				d.Encode()
				if setDeadlineErr := connL.SetWriteDeadline(time.Now().Add(timeout)); setDeadlineErr != nil {
					t.Error(setDeadlineErr)
				}
				if _, writeErr := connL.Write(d.Raw); writeErr != nil {
					t.Error(writeErr)
				}
			}()
			buf := make([]byte, 1500)
			if setDeadlineErr := p.SetReadDeadline(time.Now().Add(timeout)); setDeadlineErr != nil {
				t.Fatal(setDeadlineErr)
			}
			readN, readErr := p.Read(buf)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(buf[:readN], sent) {
				t.Error("data mismatch")
			}
			if err := p.Close(); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if err := p.Close(); err != nil {
				t.Errorf("unexpected error during second close: %v", err)
			}
			testutil.EnsureNoErrors(t, logs)
		})
	})
	t.Run("Authenticated", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		logger := zap.New(core)
		connL, connR := net.Pipe()
		connL.Close()
		stunClient := &testSTUN{}
		c, createErr := NewClient(ClientOptions{
			Log:  logger,
			Conn: connR, // should not be used
			STUN: stunClient,

			Username: "user",
			Password: "secret",
		})
		integrity := stun.NewLongTermIntegrity("user", "realm", "secret")
		serverNonce := stun.NewNonce("nonce")
		if createErr != nil {
			t.Fatal(createErr)
		}
		stunClient.indicate = func(m *stun.Message) error {
			t.Fatal("should not be called")
			return nil
		}
		stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
			var (
				nonce    stun.Nonce
				username stun.Username
			)
			if m.Type != AllocateRequest {
				t.Errorf("bad request type: %s", m.Type)
			}
			t.Logf("do: %s", m)
			if parseErr := m.Parse(&nonce, &username); parseErr != nil {
				f(stun.Event{
					Message: stun.MustBuild(m, stun.NewType(stun.MethodAllocate, stun.ClassErrorResponse),
						stun.NewRealm("realm"),
						serverNonce,
						stun.CodeUnauthorised,
						stun.Fingerprint,
					),
				})
				return nil
			}
			if !bytes.Equal(nonce, serverNonce) {
				t.Error("nonces not equal")
			}
			if integrityErr := integrity.Check(m); integrityErr != nil {
				t.Errorf("integrity check failed: %v", integrityErr)
			}
			f(stun.Event{
				Message: stun.MustBuild(m, stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse),
					&RelayedAddress{
						Port: 1113,
						IP:   net.IPv4(127, 0, 0, 2),
					},
					integrity,
					stun.Fingerprint,
				),
			})
			return nil
		}
		a, allocErr := c.Allocate()
		if allocErr != nil {
			t.Fatal(allocErr)
		}
		peer := &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 1001,
		}
		stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
			if m.Type != stun.NewType(stun.MethodCreatePermission, stun.ClassRequest) {
				t.Errorf("bad request type: %s", m.Type)
			}
			var (
				nonce    stun.Nonce
				username stun.Username
			)
			if parseErr := m.Parse(&nonce, &username); parseErr != nil {
				return parseErr
			}
			if !bytes.Equal(nonce, serverNonce) {
				t.Error("nonces not equal")
			}
			if integrityErr := integrity.Check(m); integrityErr != nil {
				t.Errorf("integrity check failed: %v", integrityErr)
			}
			f(stun.Event{
				Message: stun.MustBuild(m, stun.NewType(m.Type.Method, stun.ClassSuccessResponse),
					integrity,
					stun.Fingerprint,
				),
			})
			return nil
		}
		p, permErr := a.CreateUDP(peer)
		if permErr != nil {
			t.Fatal(permErr)
		}
		stunClient.do = func(m *stun.Message, f func(e stun.Event)) error {
			t.Fatal("should not be called")
			return nil
		}
		stunClient.indicate = func(m *stun.Message) error {
			if m.Type != stun.NewType(stun.MethodSend, stun.ClassIndication) {
				t.Errorf("bad request type: %s", m.Type)
			}
			var (
				data     Data
				peerAddr PeerAddress
			)
			if err := m.Parse(&data, &peerAddr); err != nil {
				return err
			}
			go c.stunHandler(stun.Event{
				Message: stun.MustBuild(stun.TransactionID,
					stun.NewType(stun.MethodData, stun.ClassIndication),
					data, peerAddr,
					stun.Fingerprint,
				),
			})
			return nil
		}
		sent := []byte{1, 2, 3, 4}
		if _, writeErr := p.Write(sent); writeErr != nil {
			t.Fatal(writeErr)
		}
		buf := make([]byte, 1500)
		n, readErr := p.Read(buf)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(buf[:n], sent) {
			t.Error("data mismatch")
		}
		testutil.EnsureNoErrors(t, logs)
	})
}
