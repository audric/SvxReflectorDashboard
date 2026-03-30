package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// TCP message types (from SvxReflector source: ReflectorMsg.h)
const (
	MsgTypeHeartbeat     uint16 = 1
	MsgTypeProtoVer      uint16 = 5
	MsgTypeAuthChallenge uint16 = 10
	MsgTypeAuthResponse  uint16 = 11
	MsgTypeAuthOk        uint16 = 12
	MsgTypeError         uint16 = 13
	MsgTypeServerInfo    uint16 = 100
	MsgTypeNodeJoined    uint16 = 102
	MsgTypeNodeLeft      uint16 = 103
	MsgTypeTalkerStart   uint16 = 104
	MsgTypeTalkerStop    uint16 = 105
	MsgTypeSelectTG      uint16 = 106
	MsgTypeNodeInfo      uint16 = 111
)

// UDP message types
const (
	UDPMsgTypeHeartbeat uint16 = 1
	UDPMsgTypeAudio     uint16 = 101
)

// Protocol version
const (
	ProtoMajor uint16 = 2
	ProtoMinor uint16 = 0
)

// TCPMessage represents a framed TCP message.
type TCPMessage struct {
	Type    uint16
	Payload []byte
}

func ReadTCPMessage(r io.Reader) (*TCPMessage, error) {
	var msgLen uint32
	if err := binary.Read(r, binary.BigEndian, &msgLen); err != nil {
		return nil, fmt.Errorf("read msg length: %w", err)
	}
	if msgLen < 2 {
		return nil, fmt.Errorf("message too short: %d bytes", msgLen)
	}
	if msgLen > 1<<20 {
		return nil, fmt.Errorf("message too large: %d bytes", msgLen)
	}

	data := make([]byte, msgLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read msg body: %w", err)
	}

	msgType := binary.BigEndian.Uint16(data[0:2])
	return &TCPMessage{
		Type:    msgType,
		Payload: data[2:],
	}, nil
}

func WriteTCPMessage(w io.Writer, msgType uint16, payload []byte) error {
	msgLen := uint32(2 + len(payload))
	buf := make([]byte, 4+msgLen)
	binary.BigEndian.PutUint32(buf[0:4], msgLen)
	binary.BigEndian.PutUint16(buf[4:6], msgType)
	copy(buf[6:], payload)
	_, err := w.Write(buf)
	return err
}

func BuildProtoVer(major, minor uint16) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], major)
	binary.BigEndian.PutUint16(buf[2:4], minor)
	return buf
}

func ParseAuthChallenge(payload []byte) ([]byte, error) {
	r := bytes.NewReader(payload)
	return readByteVector(r)
}

func BuildAuthResponse(callsign string, digest []byte) []byte {
	buf := new(bytes.Buffer)
	writeString(buf, callsign)
	writeByteVector(buf, digest)
	return buf.Bytes()
}

func ParseServerInfo(payload []byte) (clientID uint16, codecs []string, err error) {
	r := bytes.NewReader(payload)

	var reserved uint16
	if err := binary.Read(r, binary.BigEndian, &reserved); err != nil {
		return 0, nil, fmt.Errorf("read reserved: %w", err)
	}
	if err := binary.Read(r, binary.BigEndian, &clientID); err != nil {
		return 0, nil, fmt.Errorf("read client_id: %w", err)
	}
	if _, err := readStringVector(r); err != nil {
		return clientID, nil, nil
	}
	codecs, _ = readStringVector(r)
	return clientID, codecs, nil
}

func BuildNodeInfoV2(jsonStr string) []byte {
	buf := new(bytes.Buffer)
	writeString(buf, jsonStr)
	return buf.Bytes()
}

func BuildSelectTG(tg uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, tg)
	return buf
}

func BuildTalkerStart(tg uint32, callsign string) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, tg)
	writeString(buf, callsign)
	return buf.Bytes()
}

func BuildTalkerStop(tg uint32, callsign string) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, tg)
	writeString(buf, callsign)
	return buf.Bytes()
}

func BuildUDPHeartbeatV2(clientID uint16, seq uint16) []byte {
	buf := make([]byte, 6)
	binary.BigEndian.PutUint16(buf[0:2], UDPMsgTypeHeartbeat)
	binary.BigEndian.PutUint16(buf[2:4], clientID)
	binary.BigEndian.PutUint16(buf[4:6], seq)
	return buf
}

func BuildUDPAudioV2(clientID, seq uint16, opusData []byte) []byte {
	buf := make([]byte, 8+len(opusData))
	binary.BigEndian.PutUint16(buf[0:2], UDPMsgTypeAudio)
	binary.BigEndian.PutUint16(buf[2:4], clientID)
	binary.BigEndian.PutUint16(buf[4:6], seq)
	binary.BigEndian.PutUint16(buf[6:8], uint16(len(opusData)))
	copy(buf[8:], opusData)
	return buf
}

// --- Serialization helpers ---

func writeString(buf *bytes.Buffer, s string) {
	b := []byte(s)
	binary.Write(buf, binary.BigEndian, uint16(len(b)))
	buf.Write(b)
}

func readString(r *bytes.Reader) (string, error) {
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func writeByteVector(buf *bytes.Buffer, data []byte) {
	binary.Write(buf, binary.BigEndian, uint16(len(data)))
	buf.Write(data)
}

func readByteVector(r *bytes.Reader) ([]byte, error) {
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func readStringVector(r *bytes.Reader) ([]string, error) {
	var count uint16
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, err
	}
	result := make([]string, 0, count)
	for i := uint16(0); i < count; i++ {
		s, err := readString(r)
		if err != nil {
			return result, err
		}
		result = append(result, s)
	}
	return result, nil
}
