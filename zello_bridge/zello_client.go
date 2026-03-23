package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

const (
	ZelloWSURL          = "wss://zello.io/ws"
	ZelloCodec          = "opus"
	ZelloSampleRate     = 16000
	ZelloFrameDuration  = 60 // ms
	ZelloFramesPerPkt   = 1
)

// ZelloClient manages a WebSocket connection to a Zello channel.
type ZelloClient struct {
	username        string
	password        string
	channel         string
	channelPassword string
	issuerID        string
	privateKey      *rsa.PrivateKey

	conn  *websocket.Conn
	seqNo uint32

	// RX callbacks
	onStreamStart func(streamID uint32, senderName string)
	onStreamData  func(streamID uint32, packetID uint32, data []byte)
	onStreamStop  func(streamID uint32)

	// TX state
	txStreamID uint32
	txPacketID uint32
	txActive   atomic.Bool

	// Response channel for commands that need a reply
	responseCh chan map[string]interface{}

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewZelloClient(username, password, channel, channelPassword, issuerID, privateKeyPath string) (*ZelloClient, error) {
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from private key")
	}

	var privKey *rsa.PrivateKey
	// Try PKCS8 first, then PKCS1
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		privKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
	} else {
		var ok bool
		privKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
	}

	return &ZelloClient{
		username:        username,
		password:        password,
		channel:         channel,
		channelPassword: channelPassword,
		issuerID:        issuerID,
		privateKey:      privKey,
	}, nil
}

func (c *ZelloClient) SetStreamStartCallback(cb func(streamID uint32, senderName string)) {
	c.onStreamStart = cb
}

func (c *ZelloClient) SetStreamDataCallback(cb func(streamID uint32, packetID uint32, data []byte)) {
	c.onStreamData = cb
}

func (c *ZelloClient) SetStreamStopCallback(cb func(streamID uint32)) {
	c.onStreamStop = cb
}

func (c *ZelloClient) Done() <-chan struct{} {
	return c.done
}

func (c *ZelloClient) generateToken() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": c.issuerID,
		"exp": now.Add(60 * time.Second).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(c.privateKey)
}

func (c *ZelloClient) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.responseCh = make(chan map[string]interface{}, 1)
	c.seqNo = 0
	c.mu.Unlock()

	log.Printf("[Zello] Connecting to %s channel=%s user=%s...", ZelloWSURL, c.channel, c.username)

	authToken, err := c.generateToken()
	if err != nil {
		return fmt.Errorf("generate JWT: %w", err)
	}

	u, _ := url.Parse(ZelloWSURL)
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	c.conn = conn

	// Send logon
	seq := c.nextSeq()
	logon := map[string]interface{}{
		"command":    "logon",
		"seq":        seq,
		"auth_token": authToken,
		"username":   c.username,
		"password":   c.password,
		"channel":    c.channel,
	}
	if c.channelPassword != "" {
		logon["channel_password"] = c.channelPassword
		log.Printf("[Zello] Logon includes channel_password (len=%d)", len(c.channelPassword))
	}
	logonJSON, _ := json.Marshal(logon)
	log.Printf("[Zello] TX logon: %s", string(logonJSON))
	if err := c.conn.WriteJSON(logon); err != nil {
		return fmt.Errorf("send logon: %w", err)
	}

	// Read logon response
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read logon response: %w", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(msg, &resp); err != nil {
		return fmt.Errorf("parse logon response: %w", err)
	}

	if success, ok := resp["success"].(bool); !ok || !success {
		errMsg := "unknown"
		if e, ok := resp["error"].(string); ok {
			errMsg = e
		}
		return fmt.Errorf("logon failed: %s", errMsg)
	}

	log.Printf("[Zello] Logged in to channel %q", c.channel)
	return nil
}

// StartStream begins a new outgoing audio stream. Returns the stream ID.
// The response is received via RunReader which dispatches to responseCh.
func (c *ZelloClient) StartStream() (uint32, error) {
	// Codec header: [2B sample_rate LE][1B frames_per_pkt][1B frame_duration_ms]
	codecHeader := make([]byte, 4)
	binary.LittleEndian.PutUint16(codecHeader[0:2], ZelloSampleRate)
	codecHeader[2] = ZelloFramesPerPkt
	codecHeader[3] = ZelloFrameDuration

	seq := c.nextSeq()
	cmd := map[string]interface{}{
		"command":         "start_stream",
		"seq":             seq,
		"type":            "audio",
		"codec":           ZelloCodec,
		"codec_header":    codecHeader,
		"packet_duration": ZelloFrameDuration,
	}

	// Drain any stale response
	select {
	case <-c.responseCh:
	default:
	}

	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return 0, fmt.Errorf("not connected")
	}
	err := c.conn.WriteJSON(cmd)
	c.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("send start_stream: %w", err)
	}

	// Wait for RunReader to deliver the response
	select {
	case resp := <-c.responseCh:
		if success, ok := resp["success"].(bool); ok && !success {
			errMsg, _ := resp["error"].(string)
			return 0, fmt.Errorf("start_stream failed: %s", errMsg)
		}
		streamID := uint32(0)
		if sid, ok := resp["stream_id"].(float64); ok {
			streamID = uint32(sid)
		}
		if streamID == 0 {
			return 0, fmt.Errorf("no stream_id in response")
		}
		c.mu.Lock()
		c.txStreamID = streamID
		c.txPacketID = 0
		c.txActive.Store(true)
		c.mu.Unlock()
		log.Printf("[Zello] TX stream started (id=%d)", streamID)
		return streamID, nil

	case <-time.After(5 * time.Second):
		return 0, fmt.Errorf("start_stream response timeout")

	case <-c.done:
		return 0, fmt.Errorf("connection closed")
	}
}

// SendAudio sends a single OPUS audio frame.
func (c *ZelloClient) SendAudio(opusData []byte) error {
	if !c.txActive.Load() {
		return nil
	}

	c.mu.Lock()
	streamID := c.txStreamID
	packetID := c.txPacketID
	c.txPacketID++
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	// Binary frame: [1B type=1][4B stream_id BE][4B packet_id BE][OPUS data]
	pkt := make([]byte, 9+len(opusData))
	pkt[0] = 1 // audio type
	binary.BigEndian.PutUint32(pkt[1:5], streamID)
	binary.BigEndian.PutUint32(pkt[5:9], packetID)
	copy(pkt[9:], opusData)

	return conn.WriteMessage(websocket.BinaryMessage, pkt)
}

// StopStream ends the current outgoing audio stream.
func (c *ZelloClient) StopStream() error {
	c.txActive.Store(false)

	c.mu.Lock()
	streamID := c.txStreamID
	conn := c.conn
	c.txStreamID = 0
	c.mu.Unlock()

	if conn == nil || streamID == 0 {
		return nil
	}

	seq := c.nextSeq()
	cmd := map[string]interface{}{
		"command":   "stop_stream",
		"stream_id": streamID,
		"seq":       seq,
	}

	c.mu.Lock()
	err := c.conn.WriteJSON(cmd)
	c.mu.Unlock()

	log.Printf("[Zello] TX stream stopped (id=%d)", streamID)
	return err
}

// RunReader reads incoming WebSocket messages and dispatches events.
func (c *ZelloClient) RunReader() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.done)
		}
		c.mu.Unlock()
	}()

	for {
		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			if !c.isClosed() {
				log.Printf("[Zello] Read error: %v", err)
			}
			return
		}

		if msgType == websocket.TextMessage {
			c.handleTextMessage(data)
		} else if msgType == websocket.BinaryMessage {
			c.handleBinaryMessage(data)
		}
	}
}

func (c *ZelloClient) handleTextMessage(data []byte) {
	log.Printf("[Zello] RX text: %s", string(data))

	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	// Route responses (messages with "success" or "error" fields) to responseCh
	if _, hasSuccess := msg["success"]; hasSuccess {
		select {
		case c.responseCh <- msg:
		default:
			log.Printf("[Zello] Response dropped (no waiter): %s", string(data))
		}
		return
	}
	if _, hasStreamID := msg["stream_id"]; hasStreamID {
		if _, hasCommand := msg["command"]; !hasCommand {
			// Response to start_stream (has stream_id but no command)
			select {
			case c.responseCh <- msg:
			default:
			}
			return
		}
	}

	command, _ := msg["command"].(string)
	switch command {
	case "on_stream_start":
		streamID := uint32(0)
		if sid, ok := msg["stream_id"].(float64); ok {
			streamID = uint32(sid)
		}
		sender := ""
		if from, ok := msg["from"].(string); ok {
			sender = from
		}
		log.Printf("[Zello] RX stream start: id=%d from=%s", streamID, sender)
		if c.onStreamStart != nil {
			c.onStreamStart(streamID, sender)
		}

	case "on_stream_stop":
		streamID := uint32(0)
		if sid, ok := msg["stream_id"].(float64); ok {
			streamID = uint32(sid)
		}
		log.Printf("[Zello] RX stream stop: id=%d", streamID)
		if c.onStreamStop != nil {
			c.onStreamStop(streamID)
		}
	}
}

func (c *ZelloClient) handleBinaryMessage(data []byte) {
	if len(data) < 9 {
		return
	}
	dataType := data[0]
	if dataType != 1 { // only audio
		return
	}

	streamID := binary.BigEndian.Uint32(data[1:5])
	packetID := binary.BigEndian.Uint32(data[5:9])
	opusData := data[9:]

	if c.onStreamData != nil && len(opusData) > 0 {
		c.onStreamData(streamID, packetID, opusData)
	}
}

func (c *ZelloClient) nextSeq() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seqNo++
	return c.seqNo
}

func (c *ZelloClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *ZelloClient) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	conn := c.conn
	c.mu.Unlock()

	if conn != nil {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		conn.Close()
	}
}

// ParsePrivateKeyEnvOrFile reads a PEM private key from an env var or file path.
func ParsePrivateKeyEnvOrFile(envKey, filePath string) (string, error) {
	// Check env var first (may contain the PEM directly)
	if val := os.Getenv(envKey); val != "" && strings.Contains(val, "BEGIN") {
		tmpFile := "/tmp/zello_private_key.pem"
		if err := os.WriteFile(tmpFile, []byte(val), 0600); err != nil {
			return "", err
		}
		return tmpFile, nil
	}
	// Otherwise use file path
	if filePath != "" {
		if _, err := os.Stat(filePath); err == nil {
			return filePath, nil
		}
	}
	return "", fmt.Errorf("no private key found in env %s or file %s", envKey, filePath)
}
