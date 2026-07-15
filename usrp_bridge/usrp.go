package main

import (
	"encoding/binary"
)

// USRP is the simple UDP protocol used by DVSwitch components (Analog_Bridge,
// MMDVM_Bridge) and DL1HRC's svxlink UsrpLogic. Every packet is a 32-byte
// header followed by a payload. Voice payloads carry raw 8 kHz 16-bit PCM
// (160 samples / 320 bytes per 20 ms frame). Metadata rides in a TEXT packet
// carrying a TLV_TAG_SET_INFO record with the talker callsign/talkgroup.
//
// Quirk of the protocol: the header integer fields are big-endian (htonl,
// network byte order) while the audio samples are little-endian int16.
const (
	USRPHeaderSize   = 32
	USRPMagic        = "USRP"
	USRPAudioSamples = 160 // 20 ms at 8 kHz
	USRPAudioBytes   = USRPAudioSamples * 2

	USRPTypeVoice = 0
	USRPTypeDTMF  = 1
	USRPTypeText  = 2
	USRPTypePing  = 3
	USRPTypeTLV   = 4

	// TLV tag for a "set info" metadata record (DVSwitch Analog_Bridge
	// convention). The value carries source/repeater IDs, talkgroup, timeslot,
	// color code and a null-terminated callsign.
	tlvTagSetInfo = 0x08
)

// USRPHeader is the decoded 32-byte packet header.
type USRPHeader struct {
	Seq       uint32
	Keyup     bool
	Talkgroup uint32
	Type      uint32
}

// parseHeader decodes the 32-byte header. Returns false if the packet is too
// short or lacks the "USRP" magic.
func parseHeader(buf []byte) (USRPHeader, bool) {
	var h USRPHeader
	if len(buf) < USRPHeaderSize || string(buf[0:4]) != USRPMagic {
		return h, false
	}
	h.Seq = binary.BigEndian.Uint32(buf[4:8])
	h.Keyup = binary.BigEndian.Uint32(buf[12:16]) != 0
	h.Talkgroup = binary.BigEndian.Uint32(buf[16:20])
	h.Type = binary.BigEndian.Uint32(buf[20:24])
	return h, true
}

// writeHeader fills the first 32 bytes of buf with a USRP header. Unused fields
// (memory, mpxid, reserved) are left as zero.
func writeHeader(buf []byte, seq uint32, keyup bool, tg, msgType uint32) {
	copy(buf[0:4], USRPMagic)
	binary.BigEndian.PutUint32(buf[4:8], seq)
	if keyup {
		binary.BigEndian.PutUint32(buf[12:16], 1)
	}
	binary.BigEndian.PutUint32(buf[16:20], tg)
	binary.BigEndian.PutUint32(buf[20:24], msgType)
}

// buildVoice builds a TYPE_VOICE packet carrying up to 160 little-endian PCM
// samples. keyup marks whether PTT is active.
func buildVoice(seq uint32, keyup bool, tg uint32, pcm []int16) []byte {
	buf := make([]byte, USRPHeaderSize+len(pcm)*2)
	writeHeader(buf, seq, keyup, tg, USRPTypeVoice)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[USRPHeaderSize+i*2:], uint16(s))
	}
	return buf
}

// buildStop builds the end-of-transmission packet: a header-only TYPE_VOICE
// frame with keyup=0. DVSwitch treats this as PTT release.
func buildStop(seq uint32, tg uint32) []byte {
	buf := make([]byte, USRPHeaderSize)
	writeHeader(buf, seq, false, tg, USRPTypeVoice)
	return buf
}

// parseVoice extracts little-endian PCM samples from a TYPE_VOICE payload.
func parseVoice(buf []byte) []int16 {
	payload := buf[USRPHeaderSize:]
	n := len(payload) / 2
	if n == 0 {
		return nil
	}
	pcm := make([]int16, n)
	for i := 0; i < n; i++ {
		pcm[i] = int16(binary.LittleEndian.Uint16(payload[i*2:]))
	}
	return pcm
}

// buildMetadata builds a TYPE_TEXT packet carrying a TLV_TAG_SET_INFO record so
// the DVSwitch/MMDVM side can display who is talking. Source and repeater IDs
// are left zero (we have no DMR IDs); the callsign and talkgroup are what
// matter for display.
//
// TLV value layout (bytes after tag+len):
//
//	[0..2]  source ID   (3 bytes, big-endian)
//	[3..6]  repeater ID (4 bytes, big-endian)
//	[7..9]  talkgroup   (3 bytes, big-endian)
//	[10]    timeslot
//	[11]    color code
//	[12..]  callsign (null-terminated ASCII)
func buildMetadata(seq uint32, tg uint32, callsign string) []byte {
	cs := []byte(callsign)
	value := make([]byte, 12+len(cs)+1)
	// source ID (0), repeater ID (0) left zero
	value[7] = byte(tg >> 16)
	value[8] = byte(tg >> 8)
	value[9] = byte(tg)
	copy(value[12:], cs) // trailing byte stays 0 → null terminator

	buf := make([]byte, USRPHeaderSize+2+len(value))
	writeHeader(buf, seq, false, tg, USRPTypeText)
	buf[USRPHeaderSize] = tlvTagSetInfo
	buf[USRPHeaderSize+1] = byte(len(value))
	copy(buf[USRPHeaderSize+2:], value)
	return buf
}

// parseMetadataCallsign extracts the callsign from a TYPE_TEXT payload. It
// supports the TLV_TAG_SET_INFO record above; if the payload is a plain text
// string instead, it returns it trimmed. Returns "" when nothing usable found.
func parseMetadataCallsign(buf []byte) string {
	payload := buf[USRPHeaderSize:]
	if len(payload) == 0 {
		return ""
	}
	// TLV form: tag, length, value...
	if payload[0] == tlvTagSetInfo && len(payload) >= 2 {
		length := int(payload[1])
		value := payload[2:]
		if length <= len(value) {
			value = value[:length]
		}
		if len(value) > 12 {
			return cString(value[12:])
		}
		return ""
	}
	// Plain text fallback.
	return cString(payload)
}

// cString returns the bytes up to the first NUL as a string.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
