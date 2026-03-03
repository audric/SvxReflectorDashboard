package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// All multi-byte fields use big-endian (network byte order) per Async::Msg.

// TCP message types (from SvxReflector source: ReflectorMsg.h)
const (
	MsgTypeHeartbeat              uint16 = 1
	MsgTypeProtoVer               uint16 = 5
	MsgTypeAuthChallenge          uint16 = 10
	MsgTypeAuthResponse           uint16 = 11
	MsgTypeAuthOk                 uint16 = 12
	MsgTypeStartEncryptionRequest uint16 = 14
	MsgTypeStartEncryption        uint16 = 15
	MsgTypeClientCsrRequest       uint16 = 16
	MsgTypeClientCsr              uint16 = 17
	MsgTypeClientCert             uint16 = 18
	MsgTypeCAInfo                 uint16 = 19
	MsgTypeServerInfo             uint16 = 100
	MsgTypeNodeJoined             uint16 = 102
	MsgTypeNodeLeft               uint16 = 103
	MsgTypeTalkerStart            uint16 = 104
	MsgTypeTalkerStop             uint16 = 105
	MsgTypeSelectTG               uint16 = 106
	MsgTypeNodeInfo               uint16 = 111
	MsgTypeStartUdpEncryption     uint16 = 114
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

// UDP cipher constants (from ReflectorMsg.h UdpCipher namespace)
const (
	CipherIVLen   = 12
	CipherIVRand  = 6 // IVRANDLEN = IVLEN - sizeof(uint32) - sizeof(uint16) = 12 - 4 - 2
	CipherTagLen  = 8
	CipherAADLen  = 4 // type(2) + client_id(2)
	CipherKeyLen  = 16
)

// TCPMessage represents a framed TCP message.
// Wire format: [4B size BE][payload: [2B type BE][fields...]]
type TCPMessage struct {
	Type    uint16
	Payload []byte
}

// ReadTCPMessage reads a single framed TCP message.
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

// WriteTCPMessage writes a framed TCP message.
func WriteTCPMessage(w io.Writer, msgType uint16, payload []byte) error {
	msgLen := uint32(2 + len(payload))
	buf := make([]byte, 4+msgLen)
	binary.BigEndian.PutUint32(buf[0:4], msgLen)
	binary.BigEndian.PutUint16(buf[4:6], msgType)
	copy(buf[6:], payload)
	_, err := w.Write(buf)
	return err
}

// BuildProtoVer builds payload for MsgProtoVer.
func BuildProtoVer(major, minor uint16) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], major)
	binary.BigEndian.PutUint16(buf[2:4], minor)
	return buf
}

// ParseProtoVer parses a MsgProtoVer payload.
func ParseProtoVer(payload []byte) (major, minor uint16, err error) {
	if len(payload) < 4 {
		return 0, 0, fmt.Errorf("proto ver payload too short: %d", len(payload))
	}
	major = binary.BigEndian.Uint16(payload[0:2])
	minor = binary.BigEndian.Uint16(payload[2:4])
	return major, minor, nil
}

// ParseAuthChallenge extracts the challenge nonce (vector<uint8_t>).
func ParseAuthChallenge(payload []byte) ([]byte, error) {
	r := bytes.NewReader(payload)
	return readByteVector(r)
}

// BuildAuthResponse builds payload for MsgAuthResponse.
// Field order: callsign (string), digest (vector<uint8_t>).
func BuildAuthResponse(callsign string, digest []byte) []byte {
	buf := new(bytes.Buffer)
	writeString(buf, callsign)
	writeByteVector(buf, digest)
	return buf.Bytes()
}

// ParseServerInfo extracts fields from MsgServerInfo.
// Fields: reserved (uint16), client_id (uint16), nodes (vector<string>), codecs (vector<string>).
func ParseServerInfo(payload []byte) (clientID uint16, codecs []string, err error) {
	r := bytes.NewReader(payload)

	// reserved
	var reserved uint16
	if err := binary.Read(r, binary.BigEndian, &reserved); err != nil {
		return 0, nil, fmt.Errorf("read reserved: %w", err)
	}

	// client_id
	if err := binary.Read(r, binary.BigEndian, &clientID); err != nil {
		return 0, nil, fmt.Errorf("read client_id: %w", err)
	}

	// nodes (vector<string>) — skip
	if _, err := readStringVector(r); err != nil {
		return clientID, nil, nil // ok if truncated
	}

	// codecs (vector<string>)
	codecs, _ = readStringVector(r)

	return clientID, codecs, nil
}

// ParseNodeInfo extracts cipher params from MsgNodeInfo.
// Fields: iv_rand (vector<uint8>), cipher_key (vector<uint8>), json (string).
func ParseNodeInfo(payload []byte) (ivRand []byte, cipherKey []byte, err error) {
	r := bytes.NewReader(payload)

	ivRand, err = readByteVector(r)
	if err != nil {
		return nil, nil, fmt.Errorf("read iv_rand: %w", err)
	}

	cipherKey, err = readByteVector(r)
	if err != nil {
		return nil, nil, fmt.Errorf("read cipher_key: %w", err)
	}

	return ivRand, cipherKey, nil
}

// BuildSelectTG creates payload for MsgSelectTG.
func BuildSelectTG(tg uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, tg)
	return buf
}

// BuildNodeInfo builds a MsgNodeInfo payload for V3 (with cipher params).
// Fields: iv_rand (vector<uint8>), cipher_key (vector<uint8>), json (string).
func BuildNodeInfo(ivRand, cipherKey []byte, jsonStr string) []byte {
	buf := new(bytes.Buffer)
	writeByteVector(buf, ivRand)
	writeByteVector(buf, cipherKey)
	writeString(buf, jsonStr)
	return buf.Bytes()
}

// BuildNodeInfoV2 builds a MsgNodeInfo payload for V2 (no cipher params).
// Fields: json (string).
func BuildNodeInfoV2(jsonStr string) []byte {
	buf := new(bytes.Buffer)
	writeString(buf, jsonStr)
	return buf.Bytes()
}

// EncryptedUDPPacket represents the wire format of an encrypted UDP packet.
// Wire: [2B type BE][2B client_id BE] [8B GCM tag] [ciphertext]
//        \_________AAD (4 bytes)____/
type EncryptedUDPPacket struct {
	Type     uint16
	ClientID uint16
	Tag      []byte // 8 bytes
	Data     []byte // ciphertext (decrypts to [2B seq BE][audio...])
}

// ParseEncryptedUDPPacket parses a raw encrypted UDP datagram.
func ParseEncryptedUDPPacket(data []byte) (*EncryptedUDPPacket, error) {
	minLen := CipherAADLen + CipherTagLen
	if len(data) < minLen {
		return nil, fmt.Errorf("encrypted UDP packet too short: %d (need %d)", len(data), minLen)
	}

	return &EncryptedUDPPacket{
		Type:     binary.BigEndian.Uint16(data[0:2]),
		ClientID: binary.BigEndian.Uint16(data[2:4]),
		Tag:      data[4 : 4+CipherTagLen],
		Data:     data[4+CipherTagLen:],
	}, nil
}

// AAD returns the Additional Authenticated Data (first 4 bytes of packet).
func (p *EncryptedUDPPacket) AAD(raw []byte) []byte {
	return raw[:CipherAADLen]
}

// BuildUDPHeartbeatV2 creates a V2 UDP heartbeat packet.
// V2 wire format: [2B type][2B client_id][2B seq]
func BuildUDPHeartbeatV2(clientID uint16, seq uint16) []byte {
	buf := make([]byte, 6)
	binary.BigEndian.PutUint16(buf[0:2], UDPMsgTypeHeartbeat)
	binary.BigEndian.PutUint16(buf[2:4], clientID)
	binary.BigEndian.PutUint16(buf[4:6], seq)
	return buf
}

// BuildTalkerStart creates payload for MsgTalkerStart (type 104).
// Fields: tg (uint32 BE), callsign (string).
func BuildTalkerStart(tg uint32, callsign string) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, tg)
	writeString(buf, callsign)
	return buf.Bytes()
}

// BuildTalkerStop creates payload for MsgTalkerStop (type 105).
// Fields: tg (uint32 BE), callsign (string).
func BuildTalkerStop(tg uint32, callsign string) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, tg)
	writeString(buf, callsign)
	return buf.Bytes()
}

// BuildUDPAudioV2 creates a V2 UDP audio packet.
// V2 wire format: [2B type=101][2B client_id][2B seq][2B audio_len][OPUS data]
func BuildUDPAudioV2(clientID, seq uint16, opusData []byte) []byte {
	buf := make([]byte, 8+len(opusData))
	binary.BigEndian.PutUint16(buf[0:2], UDPMsgTypeAudio)
	binary.BigEndian.PutUint16(buf[2:4], clientID)
	binary.BigEndian.PutUint16(buf[4:6], seq)
	binary.BigEndian.PutUint16(buf[6:8], uint16(len(opusData)))
	copy(buf[8:], opusData)
	return buf
}

// BuildCipherIV constructs the 12-byte IV for AES-128-GCM.
// Layout: [6B iv_rand][2B client_id BE][4B counter BE]
func BuildCipherIV(ivRand []byte, clientID uint16, counter uint32) []byte {
	iv := make([]byte, CipherIVLen)
	copy(iv[0:CipherIVRand], ivRand)
	binary.BigEndian.PutUint16(iv[CipherIVRand:CipherIVRand+2], clientID)
	binary.BigEndian.PutUint32(iv[CipherIVRand+2:CipherIVLen], counter)
	return iv
}

// --- Serialization helpers (all big-endian per Async::Msg) ---

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
