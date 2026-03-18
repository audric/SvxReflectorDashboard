package main

import "strings"

// DCS protocol constants
const (
	DCSPort = 30051

	// Packet sizes
	DCSConnectSize = 519
	DCSAckSize     = 14
	DCSDisconnSize = 11
	DCSVoiceSize   = 100

	// Voice frame cycling
	DCSFramesPerSuperframe = 21

	// Keepalive interval
	DCSKeepaliveInterval = 10 // seconds
)

// D-STAR AMBE silence frame (9 bytes)
var dstarSilenceAMBE = [9]byte{0xDC, 0x8E, 0x0A, 0x40, 0xAD, 0xED, 0xAD, 0x39, 0x6E}

// DV slow data sync bytes for frame 0
var dvSlowDataSync = [3]byte{0x55, 0x2D, 0x16}

// DV slow data filler for unused frames
var dvSlowDataFiller = [3]byte{0x16, 0x29, 0xF5}

// SlowDataEncoder holds pre-computed slow data bytes for all 21 frames in a superframe.
type SlowDataEncoder struct {
	frames [DCSFramesPerSuperframe][3]byte
}

// Frame returns the 3 slow data bytes for the given frame index (0-20).
func (e *SlowDataEncoder) Frame(id int) [3]byte {
	if id < 0 || id >= DCSFramesPerSuperframe {
		return [3]byte{}
	}
	return e.frames[id]
}

// NewSlowDataText creates a slow data encoder for a 20-character D-STAR text message.
// The message is displayed on receiving D-STAR radios.
//
// Format: 4 mini-packets of 6 bytes (1 header + 5 text), each spanning 2 voice frames.
//   - Frame 0: sync (0x55, 0x2D, 0x16)
//   - Frames 1-8: text data (4 blocks × 2 frames)
//   - Frames 9-20: filler
func NewSlowDataText(text string) *SlowDataEncoder {
	enc := &SlowDataEncoder{}

	// Pad or truncate to exactly 20 characters
	msg := make([]byte, 20)
	for i := range msg {
		msg[i] = ' '
	}
	copy(msg, []byte(text))

	// Frame 0: sync
	enc.frames[0] = dvSlowDataSync

	// 4 blocks of (1 header + 5 text bytes), each block spans 2 frames
	for block := 0; block < 4; block++ {
		header := byte(0x40 | (3 - block))
		t := msg[block*5 : block*5+5]
		f := 1 + block*2

		enc.frames[f] = [3]byte{header, t[0], t[1]}
		enc.frames[f+1] = [3]byte{t[2], t[3], t[4]}
	}

	// Frames 9-20: filler
	for i := 9; i < DCSFramesPerSuperframe; i++ {
		enc.frames[i] = dvSlowDataFiller
	}

	return enc
}

// SlowDataDecoder accumulates slow data bytes from received voice frames
// and extracts the 20-character D-STAR text message.
type SlowDataDecoder struct {
	blocks [4][5]byte // 4 blocks of 5 text bytes each
	got    [4]bool    // which blocks have been received
	buf    [6]byte    // current mini-packet accumulator (header + 5 bytes)
	bufPos int        // bytes accumulated in current mini-packet
}

// Reset clears the decoder state for a new stream.
func (d *SlowDataDecoder) Reset() {
	d.got = [4]bool{}
	d.bufPos = 0
}

// Feed processes the 3 slow data bytes from a voice frame.
// frameIndex is 0-20 within the superframe.
func (d *SlowDataDecoder) Feed(data [3]byte, frameIndex int) {
	// Frame 0 is sync — skip it
	if frameIndex == 0 {
		d.bufPos = 0
		return
	}
	// Frames 9-20 are filler
	if frameIndex > 8 {
		return
	}

	// Accumulate bytes into 6-byte mini-packets
	for _, b := range data {
		if d.bufPos < 6 {
			d.buf[d.bufPos] = b
			d.bufPos++
		}
		if d.bufPos == 6 {
			d.processMiniPacket()
			d.bufPos = 0
		}
	}
}

func (d *SlowDataDecoder) processMiniPacket() {
	header := d.buf[0]
	// Text message headers are 0x40-0x43
	if header < 0x40 || header > 0x43 {
		return
	}
	block := int(0x43 - header) // 0x43→block 0, 0x42→block 1, 0x41→block 2, 0x40→block 3
	if block < 0 || block > 3 {
		return
	}
	copy(d.blocks[block][:], d.buf[1:6])
	d.got[block] = true
}

// Text returns the decoded 20-char message if all 4 blocks have been received.
// Returns empty string if incomplete.
func (d *SlowDataDecoder) Text() string {
	for _, g := range d.got {
		if !g {
			return ""
		}
	}
	var msg [20]byte
	for i := 0; i < 4; i++ {
		copy(msg[i*5:i*5+5], d.blocks[i][:])
	}
	return strings.TrimRight(string(msg[:]), " \x00")
}

// padCallsign pads a callsign to exactly 8 characters with spaces.
func padCallsign(cs string, length int) []byte {
	b := make([]byte, length)
	for i := range b {
		b[i] = ' '
	}
	copy(b, []byte(cs))
	return b
}

// BuildDCSConnect builds a 519-byte DCS connect packet.
// callsign: 8-char padded, clientModule: local module (usually 'A'),
// reflectorModule: target module ('A'-'Z').
func BuildDCSConnect(callsign string, clientModule, reflectorModule byte) []byte {
	pkt := make([]byte, DCSConnectSize)
	copy(pkt[0:8], padCallsign(callsign, 8))
	pkt[8] = clientModule
	pkt[9] = reflectorModule
	pkt[10] = 0x0B
	return pkt
}

// BuildDCSDisconnect builds an 11-byte DCS disconnect packet.
func BuildDCSDisconnect(callsign string, clientModule byte) []byte {
	pkt := make([]byte, DCSDisconnSize)
	copy(pkt[0:8], padCallsign(callsign, 8))
	pkt[8] = clientModule
	pkt[9] = ' '
	pkt[10] = 0x00
	return pkt
}

// BuildDCSKeepalive builds a keepalive/ping packet.
// Format: callsign(7) + module(1) + 0x00 + reflector_name + 0x00 + module
func BuildDCSKeepalive(callsign string, clientModule byte, reflectorName string) []byte {
	cs := padCallsign(callsign, 7)
	refName := []byte(reflectorName)
	pkt := make([]byte, 7+1+1+len(refName)+1+1)
	copy(pkt[0:7], cs)
	pkt[7] = clientModule
	pkt[8] = 0x00
	copy(pkt[9:9+len(refName)], refName)
	pkt[9+len(refName)] = 0x00
	pkt[9+len(refName)+1] = clientModule
	return pkt
}

// DCSVoiceFrame represents a parsed DCS voice packet.
type DCSVoiceFrame struct {
	RPT2     string // gateway 2 (8 chars)
	RPT1     string // gateway 1 (8 chars)
	URCall   string // destination (8 chars, usually "CQCQCQ  ")
	MYCall   string // source callsign (8 chars)
	MYSuffix string // source suffix (4 chars, e.g. "AMBE")
	StreamID uint16
	PacketID byte // 0-20, bit 6 set = last frame
	AMBE     [9]byte
	SlowData [3]byte
	Seq      uint32 // 3-byte sequence counter
}

// IsLastFrame returns true if this is the last frame of a transmission.
func (f *DCSVoiceFrame) IsLastFrame() bool {
	return (f.PacketID & 0x40) != 0
}

// FrameIndex returns the frame number within the superframe (0-20).
func (f *DCSVoiceFrame) FrameIndex() int {
	return int(f.PacketID & 0x1F)
}

// ParseDCSVoice parses a 100-byte DCS voice packet.
func ParseDCSVoice(data []byte) *DCSVoiceFrame {
	if len(data) < DCSVoiceSize {
		return nil
	}
	if string(data[0:4]) != "0001" {
		return nil
	}

	f := &DCSVoiceFrame{}
	f.RPT2 = string(data[7:15])
	f.RPT1 = string(data[15:23])
	f.URCall = string(data[23:31])
	f.MYCall = string(data[31:39])
	f.MYSuffix = string(data[39:43])
	f.StreamID = uint16(data[43])<<8 | uint16(data[44])
	f.PacketID = data[45]
	copy(f.AMBE[:], data[46:55])
	copy(f.SlowData[:], data[55:58])
	f.Seq = uint32(data[58]) | uint32(data[59])<<8 | uint32(data[60])<<16
	return f
}

// BuildDCSVoice builds a 100-byte DCS voice packet.
func BuildDCSVoice(
	rpt2, rpt1, urCall, myCall, mySuffix string,
	streamID uint16, packetID byte,
	ambe [9]byte, slowData [3]byte, seq uint32,
) []byte {
	pkt := make([]byte, DCSVoiceSize)

	// Tag
	copy(pkt[0:4], []byte("0001"))

	// D-STAR header
	copy(pkt[7:15], padCallsign(rpt2, 8))
	copy(pkt[15:23], padCallsign(rpt1, 8))
	copy(pkt[23:31], padCallsign(urCall, 8))
	copy(pkt[31:39], padCallsign(myCall, 8))
	copy(pkt[39:43], padCallsign(mySuffix, 4))

	// Stream ID (big-endian)
	pkt[43] = byte(streamID >> 8)
	pkt[44] = byte(streamID)

	// Packet ID
	pkt[45] = packetID

	// AMBE data
	copy(pkt[46:55], ambe[:])

	// Slow data
	copy(pkt[55:58], slowData[:])

	// Sequence counter (little-endian, 3 bytes)
	pkt[58] = byte(seq)
	pkt[59] = byte(seq >> 8)
	pkt[60] = byte(seq >> 16)

	// Marker
	pkt[61] = 0x01

	return pkt
}
