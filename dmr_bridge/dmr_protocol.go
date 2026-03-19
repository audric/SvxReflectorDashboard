package main

import (
	"crypto/sha256"
	"encoding/binary"
)

// MMDVM Homebrew protocol constants
const (
	DMRDPort = 62030

	// Packet signatures
	SigRPTL = "RPTL" // Login request
	SigRPTK = "RPTK" // Auth response
	SigRPTC = "RPTC" // Config
	SigRPTP = "RPTPONG"
	SigMSTP = "MSTPING"
	SigMSTA = "MSTACK"
	SigMSTC = "MSTCL"  // Master close
	SigDMRD = "DMRD"   // Voice/data frame
	SigRPTA = "RPTACK" // Acknowledgement

	// DMRD frame size
	DMRDFrameSize = 53

	// DMR burst payload size
	BurstPayloadSize = 33

	// Voice frame types
	FrameTypeVoice     byte = 0x00
	FrameTypeVoiceSync byte = 0x01
	FrameTypeDataSync  byte = 0x02
	FrameTypeData      byte = 0x03

	// Call types
	CallTypeGroup   byte = 0x00
	CallTypePrivate byte = 0x01

	// Timeslots
	Timeslot1 byte = 0x00
	Timeslot2 byte = 0x01
)

// DMR AMBE+2 silence frame (9 bytes)
var AMBESilence = [9]byte{0xB9, 0xE8, 0x81, 0x48, 0x00, 0x00, 0x00, 0x00, 0x00}

// DMR voice sync pattern (embedded in burst A)
var VoiceSyncPattern = [7]byte{0x07, 0x55, 0xFD, 0x7D, 0xF7, 0x5F, 0x70}

// Voice LC header SYNC pattern
var DataSyncBS = [6]byte{0xD5, 0xD7, 0xF7, 0x7F, 0xD7, 0x57}

// Voice terminator SYNC pattern
var DataSyncMS = [6]byte{0x77, 0xD5, 0x5F, 0x7D, 0xFD, 0x77}

// DMR AMBE bit interleave table per ETSI TS 102 361-1 Table B.22
// Maps the 216 AMBE payload bits (from the 264-bit burst, excluding SYNC)
// into 3 frames of 72 bits each.
//
// The burst structure is:
//   bits 0-107:   AMBE part 1 (108 bits)
//   bits 108-155: SYNC/EMB (48 bits) - not AMBE
//   bits 156-263: AMBE part 2 (108 bits)
//
// The 216 AMBE bits are de-interleaved into 3 x 72-bit frames.

// deinterleaveDMR extracts 3 AMBE frames from a 264-bit DMR burst.
// Input: 33 bytes (264 bits) of burst payload.
// Output: 3 x 9-byte AMBE frames.
func deinterleaveDMR(burst [33]byte) [3][9]byte {
	var frames [3][9]byte

	// Extract 264 bits into a bit array
	var bits [264]byte
	for i := 0; i < 264; i++ {
		bits[i] = (burst[i/8] >> (7 - uint(i%8))) & 1
	}

	// Collect AMBE payload bits (skip SYNC at 108-155)
	var ambe [216]byte
	copy(ambe[0:108], bits[0:108])
	copy(ambe[108:216], bits[156:264])

	// De-interleave: AMBE bits are interleaved across 3 frames
	// Per ETSI, bit i of the AMBE payload goes to frame (i % 3), position (i / 3)
	for i := 0; i < 216; i++ {
		frameIdx := i % 3
		bitPos := i / 3
		byteIdx := bitPos / 8
		bitOffset := 7 - uint(bitPos%8)
		if ambe[i] == 1 {
			frames[frameIdx][byteIdx] |= 1 << bitOffset
		}
	}

	return frames
}

// interleaveDMR builds a 264-bit burst payload from 3 AMBE frames and SYNC/EMB data.
// syncData: 48 bits (6 bytes) for the SYNC/EMB field at positions 108-155.
func interleaveDMR(frames [3][9]byte, syncData [6]byte) [33]byte {
	// Interleave 3 frames into 216 AMBE bits
	var ambe [216]byte
	for i := 0; i < 216; i++ {
		frameIdx := i % 3
		bitPos := i / 3
		byteIdx := bitPos / 8
		bitOffset := 7 - uint(bitPos%8)
		ambe[i] = (frames[frameIdx][byteIdx] >> bitOffset) & 1
	}

	// Build 264-bit burst
	var bits [264]byte
	copy(bits[0:108], ambe[0:108])
	// SYNC/EMB at bits 108-155
	for i := 0; i < 48; i++ {
		bits[108+i] = (syncData[i/8] >> (7 - uint(i%8))) & 1
	}
	copy(bits[156:264], ambe[108:216])

	// Pack bits into bytes
	var burst [33]byte
	for i := 0; i < 264; i++ {
		if bits[i] == 1 {
			burst[i/8] |= 1 << (7 - uint(i%8))
		}
	}

	return burst
}

// ExtractAMBE extracts 3 AMBE+2 frames from a 33-byte DMR burst payload.
func ExtractAMBE(payload [33]byte) [3][9]byte {
	return deinterleaveDMR(payload)
}

// BuildAMBEPayload builds a 33-byte burst payload from 3 AMBE frames.
// burstIndex: 0-5 (A-F) determines the SYNC/EMB pattern.
// embLC: embedded LC data for bursts B-E (nil for A/F).
func BuildAMBEPayload(frames [3][9]byte, burstIndex int) [33]byte {
	var syncData [6]byte

	switch burstIndex {
	case 0: // Burst A: voice sync pattern
		// The voice sync pattern spans 48 bits at positions 108-155
		// Using the 7-byte pattern mapped to 6 bytes for the SYNC field
		copy(syncData[:], VoiceSyncPattern[:6])
	default:
		// Bursts B-E: EMB + embedded LC (simplified: use null EMB)
		// Burst F: EMB only
		// For simplicity, fill with EMB pattern (CC=0, PI=0, LCSS=0)
		// Real implementations should encode proper EMB + RC
	}

	return interleaveDMR(frames, syncData)
}

// BuildDMRDFrame builds a 53-byte DMRD voice frame.
func BuildDMRDFrame(seq byte, srcID, dstID uint32, rptID uint32, slot, callType byte,
	frameType byte, voiceSeq byte, streamID uint32, payload [33]byte) []byte {
	frame := make([]byte, DMRDFrameSize)

	// Signature
	copy(frame[0:4], SigDMRD)
	// Sequence
	frame[4] = seq
	// Source ID (24-bit BE)
	frame[5] = byte(srcID >> 16)
	frame[6] = byte(srcID >> 8)
	frame[7] = byte(srcID)
	// Destination ID (24-bit BE)
	frame[8] = byte(dstID >> 16)
	frame[9] = byte(dstID >> 8)
	frame[10] = byte(dstID)
	// Repeater ID (32-bit BE)
	binary.BigEndian.PutUint32(frame[11:15], rptID)
	// Flags: slot(1) | callType(1) | frameType(2) | voiceSeq/dataType(4)
	flags := (slot & 0x01) << 7
	flags |= (callType & 0x01) << 6
	flags |= (frameType & 0x03) << 4
	flags |= voiceSeq & 0x0F
	frame[15] = flags
	// Stream ID (32-bit BE)
	binary.BigEndian.PutUint32(frame[16:20], streamID)
	// Burst payload
	copy(frame[20:53], payload[:])

	return frame
}

// ParseDMRDFrame parses a DMRD voice frame.
type DMRDFrame struct {
	Seq       byte
	SrcID     uint32
	DstID     uint32
	RptID     uint32
	Slot      byte
	CallType  byte
	FrameType byte
	VoiceSeq  byte
	StreamID  uint32
	Payload   [33]byte
}

func ParseDMRDFrame(data []byte) *DMRDFrame {
	if len(data) < DMRDFrameSize {
		return nil
	}
	if string(data[0:4]) != SigDMRD {
		return nil
	}

	f := &DMRDFrame{
		Seq: data[4],
		SrcID: uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7]),
		DstID: uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10]),
		RptID: binary.BigEndian.Uint32(data[11:15]),
	}

	flags := data[15]
	f.Slot = (flags >> 7) & 0x01
	f.CallType = (flags >> 6) & 0x01
	f.FrameType = (flags >> 4) & 0x03
	f.VoiceSeq = flags & 0x0F
	f.StreamID = binary.BigEndian.Uint32(data[16:20])
	copy(f.Payload[:], data[20:53])

	return f
}

// IsVoice returns true if this is a voice frame.
func (f *DMRDFrame) IsVoice() bool {
	return f.FrameType == FrameTypeVoice || f.FrameType == FrameTypeVoiceSync
}

// IsTerminator returns true if this is a voice terminator frame.
func (f *DMRDFrame) IsTerminator() bool {
	return f.FrameType == FrameTypeDataSync && f.VoiceSeq == 0x02
}

// IsHeader returns true if this is a voice LC header frame.
func (f *DMRDFrame) IsHeader() bool {
	return f.FrameType == FrameTypeDataSync && f.VoiceSeq == 0x01
}

// BuildLoginPacket creates an RPTL login packet.
func BuildLoginPacket(rptID uint32) []byte {
	buf := make([]byte, 8)
	copy(buf[0:4], SigRPTL)
	binary.BigEndian.PutUint32(buf[4:8], rptID)
	return buf
}

// BuildAuthPacket creates an RPTK auth response.
// nonce: 4-byte nonce from RPTACK, password: shared secret.
func BuildAuthPacket(rptID uint32, nonce []byte, password string) []byte {
	// SHA256(nonce + password)
	h := sha256.New()
	h.Write(nonce)
	h.Write([]byte(password))
	digest := h.Sum(nil)

	buf := make([]byte, 4+4+32)
	copy(buf[0:4], SigRPTK)
	binary.BigEndian.PutUint32(buf[4:8], rptID)
	copy(buf[8:40], digest)
	return buf
}

// BuildConfigPacket creates an RPTC configuration packet.
// The config string is a fixed 302-byte field describing the repeater.
func BuildConfigPacket(rptID uint32, callsign string, rxFreq, txFreq string,
	txPower, colorCode, lat, lon, height string,
	location, description, url, softwareID, packageID string) []byte {
	buf := make([]byte, 302)
	copy(buf[0:4], SigRPTC)
	binary.BigEndian.PutUint32(buf[4:8], rptID)

	// Callsign (8 bytes, space-padded)
	cs := padRight(callsign, 8)
	copy(buf[8:16], cs)

	// RX Freq (9 bytes, zero-padded)
	copy(buf[16:25], padRight(rxFreq, 9))
	// TX Freq (9 bytes, zero-padded)
	copy(buf[25:34], padRight(txFreq, 9))
	// TX Power (2 bytes)
	copy(buf[34:36], padRight(txPower, 2))
	// Color Code (2 bytes)
	copy(buf[36:38], padRight(colorCode, 2))
	// Latitude (8 bytes)
	copy(buf[38:46], padRight(lat, 8))
	// Longitude (9 bytes)
	copy(buf[46:55], padRight(lon, 9))
	// Height (3 bytes)
	copy(buf[55:58], padRight(height, 3))
	// Location (20 bytes)
	copy(buf[58:78], padRight(location, 20))
	// Description (19 bytes)
	copy(buf[78:97], padRight(description, 19))
	// URL (124 bytes)
	copy(buf[97:221], padRight(url, 124))
	// Software ID (40 bytes)
	copy(buf[221:261], padRight(softwareID, 40))
	// Package ID (40 bytes)
	copy(buf[261:301], padRight(packageID, 40))
	// Null terminator
	buf[301] = 0

	return buf
}

// BuildPongPacket creates an RPTPONG response to MSTPING.
func BuildPongPacket(rptID uint32) []byte {
	buf := make([]byte, 11)
	copy(buf[0:7], SigRPTP)
	binary.BigEndian.PutUint32(buf[7:11], rptID)
	return buf
}

// BuildVoiceLCHeader builds a DMRD frame for a Voice LC Header.
// This signals the start of a voice call.
func BuildVoiceLCHeader(seq byte, srcID, dstID, rptID, streamID uint32, slot, callType byte) []byte {
	var payload [33]byte

	// Build Full LC: PF=0, FLCO=0 (group voice), FID=0, service options=0
	var lc [12]byte
	if callType == CallTypePrivate {
		lc[0] = 0x03 // FLCO = Unit to Unit Voice Call
	}
	// Destination ID
	lc[3] = byte(dstID >> 16)
	lc[4] = byte(dstID >> 8)
	lc[5] = byte(dstID)
	// Source ID
	lc[6] = byte(srcID >> 16)
	lc[7] = byte(srcID >> 8)
	lc[8] = byte(srcID)

	// BPTC(196,96) encode the 96-bit LC into the 33-byte payload
	// Simplified: pack the LC into the payload with data sync pattern
	encodeBPTC(lc[:9], &payload)

	return BuildDMRDFrame(seq, srcID, dstID, rptID, slot, callType,
		FrameTypeDataSync, 0x01, streamID, payload)
}

// BuildVoiceTerminator builds a DMRD frame for a Voice Terminator with LC.
func BuildVoiceTerminator(seq byte, srcID, dstID, rptID, streamID uint32, slot, callType byte) []byte {
	var payload [33]byte

	// Same Full LC as header
	var lc [12]byte
	if callType == CallTypePrivate {
		lc[0] = 0x03
	}
	lc[3] = byte(dstID >> 16)
	lc[4] = byte(dstID >> 8)
	lc[5] = byte(dstID)
	lc[6] = byte(srcID >> 16)
	lc[7] = byte(srcID >> 8)
	lc[8] = byte(srcID)

	encodeBPTC(lc[:9], &payload)

	return BuildDMRDFrame(seq, srcID, dstID, rptID, slot, callType,
		FrameTypeDataSync, 0x02, streamID, payload)
}

// encodeBPTC performs a simplified BPTC(196,96) encoding of LC data into burst payload.
// This is a minimal implementation that places LC data in the correct positions.
func encodeBPTC(lc []byte, payload *[33]byte) {
	// Place LC data into the info part of the burst
	// The full BPTC encoding involves column parity and Hamming codes,
	// but for basic operation we place the data bytes and let the
	// receiver handle FEC.
	//
	// Data layout in BPTC(196,96):
	// 196 total bits = 96 data + 100 parity/reserved
	// Rows 0-12 x Cols 0-14 matrix, data at specific positions
	//
	// Simplified: embed raw LC in burst halves around the SYNC
	copy(payload[0:9], lc[:9])
	// Add data sync pattern in the middle
	copy(payload[13:19], DataSyncBS[:])
}

func padRight(s string, length int) string {
	for len(s) < length {
		s += " "
	}
	if len(s) > length {
		s = s[:length]
	}
	return s
}
