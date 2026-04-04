package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// SVXLinkClient manages the connection to the SVXReflector.
type SVXLinkClient struct {
	host         string
	port         int
	authKey      string
	callsign     string
	nodeLocation string
	sysop        string
	extraInfo    map[string]interface{}

	tcpConn  net.Conn
	udpConn  *net.UDPConn
	clientID uint16
	udpSeq   uint16

	// Callbacks
	onAudio       func(opusFrame []byte)
	onTalkerStart func(tg uint32, callsign string)
	onTalkerStop  func(tg uint32, callsign string)

	done   chan struct{} // closed when connection is lost
	mu     sync.Mutex
	closed bool
}

func NewSVXLinkClient(host string, port int, authKey, callsign, nodeLocation, sysop string) *SVXLinkClient {
	return &SVXLinkClient{
		host:         host,
		port:         port,
		authKey:      authKey,
		callsign:     callsign,
		nodeLocation: nodeLocation,
		sysop:        sysop,
	}
}

// Done returns a channel that is closed when the connection drops.
func (c *SVXLinkClient) Done() <-chan struct{} {
	return c.done
}

// SetExtraNodeInfo sets additional fields to include in the NodeInfo JSON.
func (c *SVXLinkClient) SetExtraNodeInfo(info map[string]interface{}) {
	c.extraInfo = info
}

func (c *SVXLinkClient) SetAudioCallback(cb func(opusFrame []byte)) {
	c.onAudio = cb
}

func (c *SVXLinkClient) SetTalkerStartCallback(cb func(tg uint32, callsign string)) {
	c.onTalkerStart = cb
}

func (c *SVXLinkClient) SetTalkerStopCallback(cb func(tg uint32, callsign string)) {
	c.onTalkerStop = cb
}

func (c *SVXLinkClient) readMsg() (*TCPMessage, error) {
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

func (c *SVXLinkClient) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.mu.Unlock()

	addr := net.JoinHostPort(c.host, fmt.Sprintf("%d", c.port))
	log.Printf("[SVX] Connecting to reflector at %s...", addr)

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("TCP connect: %w", err)
	}
	c.tcpConn = conn

	// Send ProtoVer 2.0
	if err := WriteTCPMessage(c.tcpConn, MsgTypeProtoVer, BuildProtoVer(ProtoMajor, ProtoMinor)); err != nil {
		return fmt.Errorf("send proto ver: %w", err)
	}

	// Read AuthChallenge
	msg, err := c.readMsg()
	if err != nil {
		return fmt.Errorf("read auth challenge: %w", err)
	}
	if msg.Type != MsgTypeAuthChallenge {
		return fmt.Errorf("expected AuthChallenge (10), got %d", msg.Type)
	}
	challenge, err := ParseAuthChallenge(msg.Payload)
	if err != nil {
		return fmt.Errorf("parse auth challenge: %w", err)
	}

	// HMAC-SHA1 auth
	mac := hmac.New(sha1.New, []byte(c.authKey))
	mac.Write(challenge)
	digest := mac.Sum(nil)

	if err := WriteTCPMessage(c.tcpConn, MsgTypeAuthResponse, BuildAuthResponse(c.callsign, digest)); err != nil {
		return fmt.Errorf("send auth response: %w", err)
	}

	// Read AuthOk
	msg, err = c.readMsg()
	if err != nil {
		return fmt.Errorf("read auth result: %w", err)
	}
	if msg.Type == MsgTypeError {
		errMsg := "(unknown)"
		if len(msg.Payload) >= 2 {
			r := bytes.NewReader(msg.Payload)
			if s, e := readString(r); e == nil {
				errMsg = s
			}
		}
		return fmt.Errorf("auth failed: %s", errMsg)
	}
	if msg.Type != MsgTypeAuthOk {
		return fmt.Errorf("auth failed: got msg type %d", msg.Type)
	}
	log.Println("[SVX] Authentication successful")

	// Read ServerInfo
	msg, err = c.readMsg()
	if err != nil {
		return fmt.Errorf("read server info: %w", err)
	}
	if msg.Type != MsgTypeServerInfo {
		return fmt.Errorf("expected ServerInfo (100), got %d", msg.Type)
	}
	clientID, codecs, err := ParseServerInfo(msg.Payload)
	if err != nil {
		return fmt.Errorf("parse server info: %w", err)
	}
	c.clientID = clientID
	log.Printf("[SVX] Client ID: %d, codecs: %v", clientID, codecs)

	// Send NodeInfo
	infoMap := map[string]string{
		"callsign":  c.callsign,
		"sw":        "DMR_Bridge",
		"swVer":     "1.0",
		"nodeClass": "dmr",
	}
	if c.nodeLocation != "" {
		infoMap["nodeLocation"] = c.nodeLocation
	}
	if c.sysop != "" {
		infoMap["sysop"] = c.sysop
	}
	// Build final JSON with extra fields (may contain non-string values like arrays)
	finalMap := make(map[string]interface{})
	for k, v := range infoMap {
		finalMap[k] = v
	}
	for k, v := range c.extraInfo {
		finalMap[k] = v
	}
	nodeJSON, _ := json.Marshal(finalMap)
	if err := WriteTCPMessage(c.tcpConn, MsgTypeNodeInfo, BuildNodeInfoV2(string(nodeJSON))); err != nil {
		return fmt.Errorf("send node info: %w", err)
	}

	// Establish UDP
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	c.udpConn, err = net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP connect: %w", err)
	}
	if _, err := c.udpConn.Write(BuildUDPHeartbeatV2(c.clientID, 0)); err != nil {
		return fmt.Errorf("UDP registration: %w", err)
	}
	c.udpSeq = 1
	log.Println("[SVX] Connected and registered")

	return nil
}

func (c *SVXLinkClient) SelectTG(tg uint32) error {
	log.Printf("[SVX] Selecting TG %d", tg)
	return WriteTCPMessage(c.tcpConn, MsgTypeSelectTG, BuildSelectTG(tg))
}

func (c *SVXLinkClient) SendTalkerStart(tg uint32, callsign string) error {
	return WriteTCPMessage(c.tcpConn, MsgTypeTalkerStart, BuildTalkerStart(tg, callsign))
}

func (c *SVXLinkClient) SendTalkerStop(tg uint32, callsign string) error {
	return WriteTCPMessage(c.tcpConn, MsgTypeTalkerStop, BuildTalkerStop(tg, callsign))
}

func (c *SVXLinkClient) SendAudio(opusFrame []byte) error {
	c.mu.Lock()
	seq := c.udpSeq
	c.udpSeq++
	c.mu.Unlock()
	_, err := c.udpConn.Write(BuildUDPAudioV2(c.clientID, seq, opusFrame))
	return err
}

func (c *SVXLinkClient) RunTCPReader() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.done)
		}
		c.mu.Unlock()
	}()
	for {
		msg, err := ReadTCPMessage(c.tcpConn)
		if err != nil {
			if !c.isClosed() {
				log.Printf("[SVX] TCP read error: %v", err)
			}
			return
		}

		switch msg.Type {
		case MsgTypeHeartbeat:
			WriteTCPMessage(c.tcpConn, MsgTypeHeartbeat, nil)
		case MsgTypeTalkerStart:
			tg, cs := parseTalkerMsg(msg.Payload)
			log.Printf("[SVX] TalkerStart TG=%d callsign=%s", tg, cs)
			if c.onTalkerStart != nil {
				c.onTalkerStart(tg, cs)
			}
		case MsgTypeTalkerStop:
			tg, cs := parseTalkerMsg(msg.Payload)
			log.Printf("[SVX] TalkerStop TG=%d callsign=%s", tg, cs)
			if c.onTalkerStop != nil {
				c.onTalkerStop(tg, cs)
			}
		}
	}
}

func (c *SVXLinkClient) RunTCPHeartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if c.isClosed() {
			return
		}
		if err := WriteTCPMessage(c.tcpConn, MsgTypeHeartbeat, nil); err != nil {
			log.Printf("[SVX] TCP heartbeat error: %v", err)
			return
		}
	}
}

func (c *SVXLinkClient) RunUDPReader() {
	buf := make([]byte, 4096)
	for {
		c.udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := c.udpConn.Read(buf)
		if err != nil {
			if c.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("[SVX] UDP read error: %v", err)
			return
		}

		raw := buf[:n]
		if n < 6 {
			continue
		}

		msgType := binary.BigEndian.Uint16(raw[0:2])
		payload := raw[6:]

		switch msgType {
		case UDPMsgTypeHeartbeat:
			// ignore
		case UDPMsgTypeAudio:
			if len(payload) > 2 {
				audioLen := binary.BigEndian.Uint16(payload[0:2])
				audioData := payload[2:]
				if int(audioLen) < len(audioData) {
					audioData = audioData[:audioLen]
				}
				if c.onAudio != nil && len(audioData) > 0 {
					c.onAudio(audioData)
				}
			}
		}
	}
}

func (c *SVXLinkClient) RunUDPHeartbeat() {
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
		if _, err := c.udpConn.Write(BuildUDPHeartbeatV2(c.clientID, seq)); err != nil {
			log.Printf("[SVX] UDP heartbeat error: %v", err)
			return
		}
	}
}

func (c *SVXLinkClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *SVXLinkClient) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	c.mu.Unlock()
	if c.udpConn != nil {
		c.udpConn.Close()
	}
	if c.tcpConn != nil {
		c.tcpConn.Close()
	}
}

// parseTalkerMsg extracts TG and callsign from TalkerStart/TalkerStop payload.
func parseTalkerMsg(payload []byte) (uint32, string) {
	if len(payload) < 6 {
		return 0, ""
	}
	tg := binary.BigEndian.Uint32(payload[0:4])
	r := bytes.NewReader(payload[4:])
	cs, _ := readString(r)
	return tg, cs
}
