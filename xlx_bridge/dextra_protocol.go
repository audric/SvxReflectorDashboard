package main

import "encoding/binary"

// DExtra protocol constants
const (
	DExtraPort = 30001

	DExtraConnectSize     = 11
	DExtraKeepaliveSize   = 9
	DExtraVoiceHeaderSize = 56
	DExtraVoiceFrameSize  = 27

	DExtraKeepaliveInterval = 3 // seconds (dextra_bridge uses 3s)
)

// BuildDExtraConnect builds an 11-byte DExtra connect packet.
// Format: callsign(8) + clientModule + reflectorModule + space
func BuildDExtraConnect(callsign string, clientModule, reflectorModule byte) []byte {
	pkt := make([]byte, DExtraConnectSize)
	copy(pkt[0:8], padCallsign(callsign, 8))
	pkt[8] = clientModule
	pkt[9] = reflectorModule
	pkt[10] = ' '
	return pkt
}

// BuildDExtraDisconnect builds an 11-byte DExtra disconnect packet.
// Format: callsign(8) + clientModule + space + space
func BuildDExtraDisconnect(callsign string, clientModule byte) []byte {
	pkt := make([]byte, DExtraConnectSize)
	copy(pkt[0:8], padCallsign(callsign, 8))
	pkt[8] = clientModule
	pkt[9] = ' '
	pkt[10] = ' '
	return pkt
}

// BuildDExtraKeepalive builds a 9-byte DExtra keepalive packet.
// Format: callsign(8) + space
func BuildDExtraKeepalive(callsign string) []byte {
	pkt := make([]byte, DExtraKeepaliveSize)
	copy(pkt[0:8], padCallsign(callsign, 8))
	pkt[8] = ' '
	return pkt
}

// BuildDExtraVoiceHeader builds a 56-byte DSVT voice header packet.
// Sent once at the start of each transmission.
func BuildDExtraVoiceHeader(rpt2, rpt1, urCall, myCall, mySuffix string, streamID uint16) []byte {
	pkt := make([]byte, DExtraVoiceHeaderSize)

	// DSVT signature and flags
	copy(pkt[0:4], []byte("DSVT"))
	pkt[4] = 0x10 // packet type: header
	pkt[5] = 0x00
	pkt[6] = 0x00
	pkt[7] = 0x00
	pkt[8] = 0x20 // D-STAR protocol
	pkt[9] = 0x00
	pkt[10] = 0x01
	pkt[11] = 0x02 // D-STAR voice stream indicator

	// Stream ID
	binary.BigEndian.PutUint16(pkt[12:14], streamID)

	// Header present flag
	pkt[14] = 0x80

	// D-STAR header flags (3 bytes)
	pkt[15] = 0x00
	pkt[16] = 0x00
	pkt[17] = 0x00

	// Callsign fields
	copy(pkt[18:26], padCallsign(rpt2, 8))
	copy(pkt[26:34], padCallsign(rpt1, 8))
	copy(pkt[34:42], padCallsign(urCall, 8))
	copy(pkt[42:50], padCallsign(myCall, 8))
	copy(pkt[50:54], padCallsign(mySuffix, 4))

	// CRC-CCITT over the D-STAR header (bytes 15..53 = 39 bytes)
	// Stored little-endian per D-STAR spec
	crc := dstarCRC(pkt[15:54])
	pkt[54] = byte(crc & 0xFF)        // low byte
	pkt[55] = byte((crc >> 8) & 0xFF) // high byte

	return pkt
}

// BuildDExtraVoiceFrame builds a 27-byte DSVT voice frame packet.
func BuildDExtraVoiceFrame(streamID uint16, packetID byte, ambe [9]byte, slowData [3]byte) []byte {
	pkt := make([]byte, DExtraVoiceFrameSize)

	// DSVT signature and flags
	copy(pkt[0:4], []byte("DSVT"))
	pkt[4] = 0x20 // packet type: voice data
	pkt[5] = 0x00
	pkt[6] = 0x00
	pkt[7] = 0x00
	pkt[8] = 0x20 // D-STAR protocol
	pkt[9] = 0x00
	pkt[10] = 0x01
	pkt[11] = 0x02 // D-STAR voice stream indicator

	// Stream ID
	binary.BigEndian.PutUint16(pkt[12:14], streamID)

	// Packet ID (frame counter 0-20, or 0x40 for last frame)
	pkt[14] = packetID

	// AMBE data
	copy(pkt[15:24], ambe[:])

	// Slow data
	copy(pkt[24:27], slowData[:])

	return pkt
}

// ParseDExtraVoiceHeader extracts D-STAR header fields from a 56-byte DSVT header packet.
func ParseDExtraVoiceHeader(data []byte) (streamID uint16, rpt2, rpt1, urCall, myCall, mySuffix string) {
	if len(data) < DExtraVoiceHeaderSize {
		return
	}
	streamID = binary.BigEndian.Uint16(data[12:14])
	rpt2 = string(data[18:26])
	rpt1 = string(data[26:34])
	urCall = string(data[34:42])
	myCall = string(data[42:50])
	mySuffix = string(data[50:54])
	return
}

// ParseDExtraVoiceFrame extracts voice data from a 27-byte DSVT voice frame packet.
func ParseDExtraVoiceFrame(data []byte) *DCSVoiceFrame {
	if len(data) < DExtraVoiceFrameSize {
		return nil
	}
	f := &DCSVoiceFrame{}
	f.StreamID = binary.BigEndian.Uint16(data[12:14])
	f.PacketID = data[14]
	copy(f.AMBE[:], data[15:24])
	copy(f.SlowData[:], data[24:27])
	return f
}

// dstarCRC computes the D-STAR CRC-CCITT (reflected polynomial 0x8408, init 0xFFFF, final XOR 0xFFFF).
func dstarCRC(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return crc ^ 0xFFFF
}
