package main

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
)

// IAX2 constants
const (
	IAX2Port = 4569

	// Frame types
	AST_FRAME_DTMF    = 1
	AST_FRAME_VOICE   = 2
	AST_FRAME_CONTROL = 4
	AST_FRAME_IAX     = 6
	AST_FRAME_TEXT    = 7

	// IAX commands (subclass for FRAME_IAX)
	IAX_COMMAND_NEW       = 1
	IAX_COMMAND_PING      = 2
	IAX_COMMAND_PONG      = 3
	IAX_COMMAND_ACK       = 4
	IAX_COMMAND_HANGUP    = 5
	IAX_COMMAND_REJECT    = 6
	IAX_COMMAND_ACCEPT    = 7
	IAX_COMMAND_AUTHREQ   = 8
	IAX_COMMAND_AUTHREP   = 9
	IAX_COMMAND_INVAL     = 10
	IAX_COMMAND_LAGRQ     = 11
	IAX_COMMAND_LAGRP     = 12
	IAX_COMMAND_REGREQ    = 13
	IAX_COMMAND_REGAUTH   = 14
	IAX_COMMAND_REGACK    = 15
	IAX_COMMAND_REGREJ    = 16
	IAX_COMMAND_REGREL    = 17
	IAX_COMMAND_VNAK      = 18
	IAX_COMMAND_CALLTOKEN = 40

	// Control subclass (for FRAME_CONTROL)
	AST_CONTROL_HANGUP  = 1
	AST_CONTROL_RING    = 2
	AST_CONTROL_RINGING = 3
	AST_CONTROL_ANSWER  = 4
	AST_CONTROL_OPTION  = 11
	AST_CONTROL_KEY     = 12 // PTT on
	AST_CONTROL_UNKEY   = 13 // PTT off

	// Codec
	AST_FORMAT_ULAW = 4

	// Auth methods
	IAX_AUTH_MD5 = 2

	// Information Element types
	IAX_IE_CALLED_NUMBER  = 1
	IAX_IE_CALLING_NUMBER = 2
	IAX_IE_CALLING_NAME   = 4
	IAX_IE_CALLED_CONTEXT = 5
	IAX_IE_USERNAME       = 6
	IAX_IE_PASSWORD       = 7
	IAX_IE_CAPABILITY     = 8
	IAX_IE_FORMAT         = 9
	IAX_IE_VERSION        = 11
	IAX_IE_AUTHMETHODS    = 14
	IAX_IE_CHALLENGE      = 15
	IAX_IE_MD5_RESULT     = 16
	IAX_IE_REFRESH        = 19
	IAX_IE_CAUSE          = 22
	IAX_IE_RR_JITTER      = 46
	IAX_IE_RR_LOSS        = 47
	IAX_IE_RR_PKTS        = 48
	IAX_IE_RR_DELAY       = 49
	IAX_IE_RR_DROPPED     = 50
	IAX_IE_RR_OOO         = 51
	IAX_IE_CALLTOKEN      = 54

	// PCM audio parameters
	PCMSampleRate = 8000
	PCMFrameSize  = 160 // 160 samples = 20ms at 8kHz
)

// IAX2FullFrame represents a parsed full IAX2 frame.
type IAX2FullFrame struct {
	SrcCallNo  uint16
	DstCallNo  uint16
	Retransmit bool
	Timestamp  uint32
	OSeqno     byte
	ISeqno     byte
	FrameType  byte
	Subclass   byte
	IEs        map[byte][]byte // parsed information elements
	RawPayload []byte          // raw payload (for voice frames)
}

// ParseFullFrame parses an IAX2 full frame.
func ParseFullFrame(data []byte) *IAX2FullFrame {
	if len(data) < 12 {
		return nil
	}
	// Check F bit (must be set for full frame)
	if data[0]&0x80 == 0 {
		return nil
	}

	f := &IAX2FullFrame{
		SrcCallNo:  binary.BigEndian.Uint16(data[0:2]) & 0x7FFF,
		Retransmit: data[2]&0x80 != 0,
		DstCallNo:  binary.BigEndian.Uint16(data[2:4]) & 0x7FFF,
		Timestamp:  binary.BigEndian.Uint32(data[4:8]),
		OSeqno:     data[8],
		ISeqno:     data[9],
		FrameType:  data[10],
		Subclass:   data[11],
		IEs:        make(map[byte][]byte),
	}

	// Parse IEs or raw payload
	payload := data[12:]
	if f.FrameType == AST_FRAME_IAX || f.FrameType == AST_FRAME_CONTROL {
		// Parse information elements
		pos := 0
		for pos+2 <= len(payload) {
			ieType := payload[pos]
			ieLen := int(payload[pos+1])
			pos += 2
			if pos+ieLen > len(payload) {
				break
			}
			ie := make([]byte, ieLen)
			copy(ie, payload[pos:pos+ieLen])
			f.IEs[ieType] = ie
			pos += ieLen
		}
	} else {
		f.RawPayload = payload
	}

	return f
}

// BuildFullFrame builds an IAX2 full frame.
func BuildFullFrame(srcCallNo, dstCallNo uint16, ts uint32, oseq, iseq, frameType, subclass byte, payload []byte) []byte {
	pkt := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint16(pkt[0:2], srcCallNo|0x8000) // F bit set
	binary.BigEndian.PutUint16(pkt[2:4], dstCallNo)
	binary.BigEndian.PutUint32(pkt[4:8], ts)
	pkt[8] = oseq
	pkt[9] = iseq
	pkt[10] = frameType
	pkt[11] = subclass
	copy(pkt[12:], payload)
	return pkt
}

// BuildMiniFrame builds an IAX2 mini frame (for ongoing audio).
func BuildMiniFrame(srcCallNo uint16, ts uint16, audioPayload []byte) []byte {
	pkt := make([]byte, 4+len(audioPayload))
	binary.BigEndian.PutUint16(pkt[0:2], srcCallNo&0x7FFF) // F bit clear
	binary.BigEndian.PutUint16(pkt[2:4], ts)
	copy(pkt[4:], audioPayload)
	return pkt
}

// BuildIE builds a single information element.
func BuildIE(ieType byte, data []byte) []byte {
	ie := make([]byte, 2+len(data))
	ie[0] = ieType
	ie[1] = byte(len(data))
	copy(ie[2:], data)
	return ie
}

// BuildIEString builds a string IE.
func BuildIEString(ieType byte, s string) []byte {
	return BuildIE(ieType, []byte(s))
}

// BuildIEUint16 builds a 2-byte IE.
func BuildIEUint16(ieType byte, v uint16) []byte {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data, v)
	return BuildIE(ieType, data)
}

// BuildIEUint32 builds a 4-byte IE.
func BuildIEUint32(ieType byte, v uint32) []byte {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, v)
	return BuildIE(ieType, data)
}

// MD5Auth computes the MD5 challenge response: MD5(challenge + password).
func MD5Auth(challenge, password string) string {
	h := md5.Sum([]byte(challenge + password))
	return fmt.Sprintf("%x", h)
}

// IEString extracts a string from an IE value.
func IEString(ies map[byte][]byte, key byte) string {
	if v, ok := ies[key]; ok {
		return string(v)
	}
	return ""
}
