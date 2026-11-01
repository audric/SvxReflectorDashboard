package main

import (
	"crypto/sha256"
	"encoding/binary"
)

// REWIND / Open DMR Terminal Protocol (ODMRTP) wire format.
//
// ODMRTP is BrandMeister's sanctioned path for non-repeater ("terminal")
// connections — DVSwitch's "STFU" tool is one implementation of it. It is the
// REWIND protocol: UDP, little-endian, authenticated with a self-care hotspot
// password. Unlike MMDVM Homebrew, audio is carried as bare AMBE "mode 33"
// frames (3 x 9 bytes), so no DMR burst interleaving/BPTC is needed — the
// vocoder output is sent verbatim.
//
// Constants and struct layouts mirror BrandMeister's Rewind.h.
const (
	// ODMRTPPort is the default UDP port for the Open DMR Terminal interface.
	ODMRTPPort = 54006

	rewindSign      = "REWIND01"
	rewindHeaderLen = 18 // 8 sign + 2 type + 2 flags + 4 seq + 2 length
	rewindKeepAlive = 5  // seconds

	// Message types (REWIND_TYPE_*).
	rewindTypeKeepAlive      = 0x0000
	rewindTypeClose          = 0x0001
	rewindTypeChallenge      = 0x0002
	rewindTypeAuthentication = 0x0003
	rewindTypeReport         = 0x0100
	rewindTypeConfiguration  = 0x0900
	rewindTypeSubscription   = 0x0901
	rewindTypeDMRDataBase    = 0x0910
	rewindTypeDMRStart       = 0x0911 // voice header w/ LC
	rewindTypeDMRTerminator  = 0x0912 // terminator w/ LC
	rewindTypeDMRAudioFrame  = 0x0920
	rewindTypeDMREmbedded    = 0x0927
	rewindTypeSuperHeader    = 0x0928
	rewindTypeFailureCode    = 0x0929

	// Flags (REWIND_FLAG_*). Real-time data (>= DMR_DATA_BASE) sets REAL_TIME_1.
	rewindFlagNone      = 0
	rewindFlagRealTime1 = 1 << 0

	// Service role: REWIND_ROLE_APPLICATION (0x20) + 1.
	rewindServiceOpenTerminal = 0x21

	// Configuration options (REWIND_OPTION_*).
	rewindOptionSuperHeader = 1 << 0

	// Session types (SESSION_TYPE_*).
	rewindSessionPrivateVoice = 5
	rewindSessionGroupVoice   = 7

	rewindCallLen = 10

	// odmrtpAudioFrameLen is one "mode 33" superframe: 3 AMBE+2 frames.
	odmrtpAudioFrameLen = 3 * AMBEFrameSize

	rewindVersionDesc = "STFU_0.3.4 Linux x86_64"
)

// rewindPacket is a parsed REWIND transport-layer packet.
type rewindPacket struct {
	Type    uint16
	Flags   uint16
	Seq     uint32
	Payload []byte
}

// buildRewind frames a REWIND packet around the given payload.
func buildRewind(msgType, flags uint16, seq uint32, payload []byte) []byte {
	buf := make([]byte, rewindHeaderLen+len(payload))
	copy(buf[0:8], rewindSign)
	binary.LittleEndian.PutUint16(buf[8:10], msgType)
	binary.LittleEndian.PutUint16(buf[10:12], flags)
	binary.LittleEndian.PutUint32(buf[12:16], seq)
	binary.LittleEndian.PutUint16(buf[16:18], uint16(len(payload)))
	copy(buf[18:], payload)
	return buf
}

// parseRewind validates and decodes a received datagram. Payload aliases data.
func parseRewind(data []byte) (*rewindPacket, bool) {
	if len(data) < rewindHeaderLen || string(data[0:8]) != rewindSign {
		return nil, false
	}
	length := int(binary.LittleEndian.Uint16(data[16:18]))
	if len(data) < rewindHeaderLen+length {
		return nil, false
	}
	return &rewindPacket{
		Type:    binary.LittleEndian.Uint16(data[8:10]),
		Flags:   binary.LittleEndian.Uint16(data[10:12]),
		Seq:     binary.LittleEndian.Uint32(data[12:16]),
		Payload: data[rewindHeaderLen : rewindHeaderLen+length],
	}, true
}

// buildVersionData is the keep-alive payload (struct RewindVersionData) that
// identifies us as an Open DMR Terminal carrying the given DMR ID.
func buildVersionData(remoteID uint32) []byte {
	desc := []byte(rewindVersionDesc)
	buf := make([]byte, 5+len(desc))
	binary.LittleEndian.PutUint32(buf[0:4], remoteID)
	buf[4] = rewindServiceOpenTerminal
	copy(buf[5:], desc)
	return buf
}

// authResponse computes the challenge response: SHA-256(salt || password).
func authResponse(salt []byte, password string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	return h.Sum(nil)
}

// buildConfigData is the RewindConfigurationData payload.
func buildConfigData(options uint32) []byte {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, options)
	return buf
}

// buildSubscriptionData is the RewindSubscriptionData payload (attach to a TG).
func buildSubscriptionData(sessionType, dstID uint32) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[0:4], sessionType)
	binary.LittleEndian.PutUint32(buf[4:8], dstID)
	return buf
}

// buildSuperHeader is the RewindSuperHeader payload announcing a TX call.
func buildSuperHeader(sessionType, srcID, dstID uint32, srcCall string) []byte {
	buf := make([]byte, 12+2*rewindCallLen)
	binary.LittleEndian.PutUint32(buf[0:4], sessionType)
	binary.LittleEndian.PutUint32(buf[4:8], srcID)
	binary.LittleEndian.PutUint32(buf[8:12], dstID)
	copy(buf[12:12+rewindCallLen], srcCall)
	return buf
}

// parseSuperHeader decodes the session type, source and destination IDs.
func parseSuperHeader(payload []byte) (sessionType, srcID, dstID uint32, ok bool) {
	if len(payload) < 12 {
		return 0, 0, 0, false
	}
	return binary.LittleEndian.Uint32(payload[0:4]),
		binary.LittleEndian.Uint32(payload[4:8]),
		binary.LittleEndian.Uint32(payload[8:12]), true
}
