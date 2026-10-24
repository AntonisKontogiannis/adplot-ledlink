// Package ddpwire encodes raw pixel buffers into DDP (Distributed Display
// Protocol) v1 UDP packets. It is shared by the plain DDP driver and by the
// reference-firmware driver.
package ddpwire

import "encoding/binary"

const (
	// DefaultPort is the standard DDP UDP port.
	DefaultPort = 4048
	// MaxPayload keeps every datagram below the typical 1500-byte MTU.
	MaxPayload = 1440
	// HeaderLen is the DDP header size in bytes.
	HeaderLen = 10
	// TypeRGB24 is the DDP data type for 24-bit RGB pixels.
	TypeRGB24 = 0x0B
	// FlagPushV1 marks a version-1 DDP packet that must be pushed to output.
	FlagPushV1 = 0x41
)

// Encoder assigns DDP sequence numbers across the packets of one or more
// streamed frames. It is not safe for concurrent use.
type Encoder struct {
	seq uint8
}

// Packets splits data into DDP packets (each carrying at most MaxPayload
// bytes) with increasing byte offsets, then advances the sequence counter so
// receivers can detect dropped packets.
func (e *Encoder) Packets(data []byte) [][]byte {
	var packets [][]byte
	for offset := 0; offset < len(data); offset += MaxPayload {
		end := offset + MaxPayload
		if end > len(data) {
			end = len(data)
		}
		payload := end - offset

		pkt := make([]byte, HeaderLen+payload)
		pkt[0] = FlagPushV1
		pkt[1] = e.seq
		e.seq++
		pkt[2] = TypeRGB24
		pkt[3] = 0 // destination id 0 = primary output
		binary.BigEndian.PutUint32(pkt[4:8], uint32(offset))
		binary.BigEndian.PutUint16(pkt[8:10], uint16(payload))
		copy(pkt[HeaderLen:], data[offset:end])
		packets = append(packets, pkt)
	}
	return packets
}

// ApplyBrightness scales RGB bytes by percent. Values >= 100 return the input
// unchanged.
func ApplyBrightness(data []byte, percent int) []byte {
	if percent >= 100 {
		return data
	}
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = uint8(int(b) * percent / 100)
	}
	return out
}
