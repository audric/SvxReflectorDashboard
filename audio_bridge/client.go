package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Client manages the connection to the SVXReflector using protocol V2 (no encryption).
type Client struct {
	host     string
	port     int
	authKey  string
	callsign string

	tcpConn  net.Conn
	udpConn  *net.UDPConn
	clientID uint16
	udpSeq   uint16 // outgoing UDP sequence number

	currentTG atomic.Uint32

	audioCallback func(opusFrame []byte)

	mu     sync.Mutex
	closed bool
}

// NewClient creates a new SVXReflector client.
func NewClient(host string, port int, authKey, callsign string) *Client {
	return &Client{
		host:     host,
		port:     port,
		authKey:  authKey,
		callsign: callsign,
	}
}

// readMsg reads the next TCP message, skipping heartbeats.
func (c *Client) readMsg() (*TCPMessage, error) {
	for {
		msg, err := ReadTCPMessage(c.tcpConn)
		if err != nil {
			return nil, err
		}
		if msg.Type == MsgTypeHeartbeat {
			WriteTCPMessage(c.tcpConn, MsgTypeHeartbeat, nil)
			continue
		}
		return msg, nil
	}
}

// Connect performs the full TCP handshake using protocol V2.
func (c *Client) Connect() error {
	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	log.Printf("Connecting to reflector at %s (proto V2)...", addr)

	// Step 1: Plain TCP connection
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("TCP connect: %w", err)
	}
	c.tcpConn = conn

	// Step 2: Send ProtoVer 2.0
	protoPayload := BuildProtoVer(ProtoMajor, ProtoMinor)
	if err := WriteTCPMessage(c.tcpConn, MsgTypeProtoVer, protoPayload); err != nil {
		return fmt.Errorf("send proto ver: %w", err)
	}
	log.Printf("Sent MsgProtoVer %d.%d", ProtoMajor, ProtoMinor)

	// Step 3: Read AuthChallenge (V2 — no TLS upgrade)
	msg, err := c.readMsg()
	if err != nil {
		return fmt.Errorf("read server response: %w", err)
	}
	if msg.Type != MsgTypeAuthChallenge {
		return fmt.Errorf("expected MsgAuthChallenge (10), got type %d", msg.Type)
	}
	challenge, err := ParseAuthChallenge(msg.Payload)
	if err != nil {
		return fmt.Errorf("parse auth challenge: %w", err)
	}
	log.Printf("Received auth challenge (%d bytes)", len(challenge))

	// Step 4: Compute HMAC-SHA1 and send auth response
	mac := hmac.New(sha1.New, []byte(c.authKey))
	mac.Write(challenge)
	digest := mac.Sum(nil)

	authPayload := BuildAuthResponse(c.callsign, digest)
	if err := WriteTCPMessage(c.tcpConn, MsgTypeAuthResponse, authPayload); err != nil {
		return fmt.Errorf("send auth response: %w", err)
	}

	// Step 5: Receive MsgAuthOk
	msg, err = c.readMsg()
	if err != nil {
		return fmt.Errorf("read auth ok: %w", err)
	}
	if msg.Type == 13 { // MsgError
		errMsg := "(unknown)"
		if len(msg.Payload) >= 2 {
			r := bytes.NewReader(msg.Payload)
			if s, e := readString(r); e == nil {
				errMsg = s
			}
		}
		return fmt.Errorf("auth failed: server error: %s", errMsg)
	}
	if msg.Type != MsgTypeAuthOk {
		return fmt.Errorf("auth failed: got msg type %d (expected 12)", msg.Type)
	}
	log.Println("Authentication successful")

	// Step 6: Receive MsgServerInfo
	msg, err = c.readMsg()
	if err != nil {
		return fmt.Errorf("read server info: %w", err)
	}
	if msg.Type != MsgTypeServerInfo {
		return fmt.Errorf("expected MsgServerInfo (100), got type %d", msg.Type)
	}
	clientID, codecs, err := ParseServerInfo(msg.Payload)
	if err != nil {
		return fmt.Errorf("parse server info: %w", err)
	}
	c.clientID = clientID
	log.Printf("Client ID: %d, codecs: %v", clientID, codecs)

	// Step 7: Send MsgNodeInfo (V2: just JSON, no cipher params)
	nodeInfoPayload := BuildNodeInfoV2(fmt.Sprintf(`{"callsign":"%s"}`, c.callsign))
	if err := WriteTCPMessage(c.tcpConn, MsgTypeNodeInfo, nodeInfoPayload); err != nil {
		return fmt.Errorf("send node info: %w", err)
	}
	log.Println("Sent MsgNodeInfo")

	// Step 8: Establish UDP connection
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP addr: %w", err)
	}
	c.udpConn, err = net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP connect: %w", err)
	}

	// Send UDP registration heartbeat: [2B type=1][2B client_id][2B seq=0]
	regPkt := BuildUDPHeartbeatV2(c.clientID, 0)
	if _, err := c.udpConn.Write(regPkt); err != nil {
		return fmt.Errorf("send UDP registration: %w", err)
	}
	c.udpSeq = 1
	log.Println("UDP connection established, registration heartbeat sent")

	return nil
}

// SelectTG sends a talkgroup selection message.
func (c *Client) SelectTG(tg uint32) error {
	c.currentTG.Store(tg)
	payload := BuildSelectTG(tg)
	log.Printf("Selecting TG %d", tg)
	return WriteTCPMessage(c.tcpConn, MsgTypeSelectTG, payload)
}

// SendTalkerStart sends a TalkerStart message for the given TG.
// If callsign is empty, falls back to the client's own callsign.
func (c *Client) SendTalkerStart(tg uint32, callsign string) error {
	if callsign == "" {
		callsign = c.callsign
	}
	payload := BuildTalkerStart(tg, callsign)
	log.Printf("TX: TalkerStart TG %d callsign %s", tg, callsign)
	return WriteTCPMessage(c.tcpConn, MsgTypeTalkerStart, payload)
}

// SendTalkerStop sends a TalkerStop message for the given TG.
// If callsign is empty, falls back to the client's own callsign.
func (c *Client) SendTalkerStop(tg uint32, callsign string) error {
	if callsign == "" {
		callsign = c.callsign
	}
	payload := BuildTalkerStop(tg, callsign)
	log.Printf("TX: TalkerStop TG %d callsign %s", tg, callsign)
	return WriteTCPMessage(c.tcpConn, MsgTypeTalkerStop, payload)
}

// SendAudio sends a single OPUS frame via UDP.
func (c *Client) SendAudio(opusFrame []byte) error {
	c.mu.Lock()
	seq := c.udpSeq
	c.udpSeq++
	c.mu.Unlock()
	pkt := BuildUDPAudioV2(c.clientID, seq, opusFrame)
	_, err := c.udpConn.Write(pkt)
	return err
}

// SetAudioCallback sets the function called for each OPUS frame.
func (c *Client) SetAudioCallback(cb func(opusFrame []byte)) {
	c.audioCallback = cb
}

// RunTCPReader reads and dispatches TCP control messages.
func (c *Client) RunTCPReader() {
	for {
		msg, err := ReadTCPMessage(c.tcpConn)
		if err != nil {
			if !c.isClosed() {
				log.Printf("TCP read error: %v", err)
			}
			return
		}

		switch msg.Type {
		case MsgTypeHeartbeat:
			WriteTCPMessage(c.tcpConn, MsgTypeHeartbeat, nil)
		case MsgTypeNodeJoined:
			log.Printf("Node joined (%d bytes)", len(msg.Payload))
		case MsgTypeNodeLeft:
			log.Printf("Node left (%d bytes)", len(msg.Payload))
		case MsgTypeTalkerStart:
			log.Printf("Talker start (%d bytes)", len(msg.Payload))
		case MsgTypeTalkerStop:
			log.Printf("Talker stop (%d bytes)", len(msg.Payload))
		default:
			log.Printf("TCP msg type=%d, %d bytes", msg.Type, len(msg.Payload))
		}
	}
}

// RunTCPHeartbeat sends TCP heartbeats periodically.
func (c *Client) RunTCPHeartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if c.isClosed() {
			return
		}
		if err := WriteTCPMessage(c.tcpConn, MsgTypeHeartbeat, nil); err != nil {
			log.Printf("TCP heartbeat error: %v", err)
			return
		}
	}
}

// RunUDPReader reads plain V2 UDP packets and dispatches audio frames.
// V2 wire format: [2B type][2B client_id][2B seq][payload...]
const udpV2HeaderLen = 6

func (c *Client) RunUDPReader() {
	log.Println("UDP reader started, waiting for V2 packets...")
	buf := make([]byte, 4096)
	var udpPktCount uint64
	for {
		c.udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := c.udpConn.Read(buf)
		if err != nil {
			if c.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Printf("UDP read timeout (no packets in 30s, total received: %d)", udpPktCount)
				continue
			}
			log.Printf("UDP read error: %v", err)
			return
		}

		udpPktCount++
		raw := buf[:n]

		// V2 header: 2B type + 2B client_id + 2B seq = 6 bytes
		if n < udpV2HeaderLen {
			continue
		}

		msgType := binary.BigEndian.Uint16(raw[0:2])
		payload := raw[udpV2HeaderLen:]

		if udpPktCount <= 5 {
			log.Printf("UDP pkt #%d: %d bytes, type=%d, payload=%d bytes",
				udpPktCount, n, msgType, len(payload))
		}

		switch msgType {
		case UDPMsgTypeHeartbeat:
			if udpPktCount <= 3 {
				log.Printf("UDP heartbeat received (pkt #%d)", udpPktCount)
			}
		case UDPMsgTypeAudio:
			// Payload: [2B vector_length][OPUS data]
			if len(payload) > 2 {
				audioLen := binary.BigEndian.Uint16(payload[0:2])
				audioData := payload[2:]
				if int(audioLen) < len(audioData) {
					audioData = audioData[:audioLen]
				}
				if udpPktCount <= 5 || udpPktCount%500 == 0 {
					log.Printf("UDP audio frame: %d bytes OPUS (pkt #%d)", len(audioData), udpPktCount)
				}
				if c.audioCallback != nil && len(audioData) > 0 {
					c.audioCallback(audioData)
				}
			}
		default:
			if udpPktCount <= 10 {
				log.Printf("UDP unknown msg type %d (%d bytes payload)", msgType, len(payload))
			}
		}
	}
}

// RunUDPHeartbeat sends V2 UDP heartbeats periodically.
func (c *Client) RunUDPHeartbeat() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if c.isClosed() {
			return
		}
		c.mu.Lock()
		seq := c.udpSeq
		c.udpSeq++
		c.mu.Unlock()
		pkt := BuildUDPHeartbeatV2(c.clientID, seq)
		if _, err := c.udpConn.Write(pkt); err != nil {
			log.Printf("UDP heartbeat error: %v", err)
			return
		}
	}
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Close tears down all connections.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	if c.udpConn != nil {
		c.udpConn.Close()
	}
	if c.tcpConn != nil {
		c.tcpConn.Close()
	}
}
