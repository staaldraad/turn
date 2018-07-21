package turn

import (
	"errors"
	"strconv"

	"github.com/gortc/stun"
)

// ChannelNumber represents CHANNEL-NUMBER attribute.
//
// The CHANNEL-NUMBER attribute contains the number of the channel.
//
// RFC 5766 Section 14.1
type ChannelNumber int // encoded as uint16

func (n ChannelNumber) String() string { return strconv.Itoa(int(n)) }

// 16 bits of uint + 16 bits of RFFU = 0.
const channelNumberSize = 4

// AddTo adds CHANNEL-NUMBER to message.
func (n ChannelNumber) AddTo(m *stun.Message) error {
	v := make([]byte, channelNumberSize)
	bin.PutUint16(v[:2], uint16(n))
	// v[2:4] are zeroes (RFFU = 0)
	m.Add(stun.AttrChannelNumber, v)
	return nil
}

// GetFrom decodes CHANNEL-NUMBER from message.
func (n *ChannelNumber) GetFrom(m *stun.Message) error {
	v, err := m.Get(stun.AttrChannelNumber)
	if err != nil {
		return err
	}
	if len(v) != channelNumberSize {
		return &BadAttrLength{
			Attr:     stun.AttrChannelNumber,
			Got:      len(v),
			Expected: channelNumberSize,
		}
	}
	_ = v[channelNumberSize-1] // asserting length
	*n = ChannelNumber(bin.Uint16(v[:2]))
	// v[2:4] is RFFU and equals to 0.
	return nil
}

// See https://tools.ietf.org/html/rfc5766#section-11:
//
// 0x4000 through 0x7FFF: These values are the allowed channel
// numbers (16,383 possible values).
const (
	minChannelNumber = 0x4000
	maxChannelNumber = 0x7FFF
)

// ErrInvalidChannelNumber means that channel number is not valid as by RFC 5766 Section 11.
var ErrInvalidChannelNumber = errors.New("channel number not in [0x4000, 0x7FFF]")

// isChannelNumberValid returns true if c in [0x4000, 0x7FFF].
func isChannelNumberValid(c uint16) bool {
	return c >= minChannelNumber && c <= maxChannelNumber
}

// Valid returns true if channel number has correct value that complies RFC 5766 Section 11 range.
func (n ChannelNumber) Valid() bool {
	return isChannelNumberValid(uint16(n))
}
