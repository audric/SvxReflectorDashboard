package main

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

// DMRClient manages the connection to a DMR master server via MMDVM Homebrew protocol.
type DMRClient struct {
	host      string
	port      int
	rptID     uint32
	password  string
	callsign  string
	talkgroup uint32
	timeslot  byte
	colorCode byte

	conn *net.UDPConn

	// TX state
	txStreamID uint32
	txSeq      byte
	txBurst    int // 0-5 (A-F) burst counter
	txBuf      [][9]byte
	txMu       sync.Mutex

	// Callbacks
	onVoice     func(srcID uint32, frames [3][9]byte)
	onCallStart func(srcID, dstID uint32)
	onCallEnd   func(srcID uint32)

	done   chan struct{}
	mu     sync.Mutex
	closed bool

	// Keepalive tracking
	missedPings int
}

func NewDMRClient(host string, port int, rptID uint32, password, callsign string,
	talkgroup uint32, timeslot, colorCode byte) *DMRClient {
	return &DMRClient{
		host:      host,
		port:      port,
		rptID:     rptID,
		password:  password,
		callsign:  callsign,
		talkgroup: talkgroup,
		timeslot:  timeslot,
		colorCode: colorCode,
	}
}

func (c *DMRClient) Done() <-chan struct{} {
	return c.done
}

func (c *DMRClient) SetVoiceCallback(cb func(srcID uint32, frames [3][9]byte)) {
	c.onVoice = cb
}

func (c *DMRClient) SetCallStartCallback(cb func(srcID, dstID uint32)) {
	c.onCallStart = cb
}

func (c *DMRClient) SetCallEndCallback(cb func(srcID uint32)) {
	c.onCallEnd = cb
}

// Connect performs the MMDVM Homebrew authentication handshake.
func (c *DMRClient) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.missedPings = 0
	c.mu.Unlock()

	addr := net.JoinHostPort(c.host, fmt.Sprintf("%d", c.port))
	log.Printf("[DMR] Connecting to master at %s...", addr)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial: %w", err)
	}
	c.conn = conn

	// Step 1: Send RPTL (login)
	if _, err := c.conn.Write(BuildLoginPacket(c.rptID)); err != nil {
		return fmt.Errorf("send RPTL: %w", err)
	}

	// Read RPTACK with nonce
	nonce, err := c.readACK("login")
	if err != nil {
		return fmt.Errorf("login ACK: %w", err)
	}
	if len(nonce) < 4 {
		return fmt.Errorf("nonce too short: %d bytes", len(nonce))
	}
	log.Printf("[DMR] Received nonce: %x", nonce[:4])

	// Step 2: Send RPTK (auth)
	if _, err := c.conn.Write(BuildAuthPacket(c.rptID, nonce[:4], c.password)); err != nil {
		return fmt.Errorf("send RPTK: %w", err)
	}

	if _, err := c.readACK("auth"); err != nil {
		return fmt.Errorf("auth ACK: %w", err)
	}
	log.Println("[DMR] Authentication successful")

	// Step 3: Send RPTC (config)
	ccStr := fmt.Sprintf("%d", c.colorCode)
	configPkt := BuildConfigPacket(c.rptID, c.callsign,
		"000000000", "000000000", // RX/TX freq (not applicable for bridge)
		"01", ccStr,
		"0.0000", "0.0000", "000", // lat, lon, height
		"DMR Bridge", "SVX-DMR Bridge", "", "DMR_Bridge", "1.0")
	if _, err := c.conn.Write(configPkt); err != nil {
		return fmt.Errorf("send RPTC: %w", err)
	}

	if _, err := c.readACK("config"); err != nil {
		return fmt.Errorf("config ACK: %w", err)
	}
	log.Println("[DMR] Config accepted, connected to master")

	return nil
}

// readACK reads a response with timeout, expecting RPTACK.
func (c *DMRClient) readACK(phase string) ([]byte, error) {
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 512)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", phase, err)
	}
	data := buf[:n]

	if n >= 6 && string(data[:6]) == SigRPTA {
		return data[6:], nil
	}
	if n >= 6 && string(data[:6]) == SigMSTA {
		return data[6:], nil
	}

	// Check for NAK
	if n >= 6 && string(data[:6]) == "MSTNAK" {
		return nil, fmt.Errorf("master NAK during %s", phase)
	}

	return nil, fmt.Errorf("unexpected response during %s: %q", phase, string(data[:min(n, 10)]))
}

// RunReader reads incoming packets from the DMR master.
func (c *DMRClient) RunReader() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.done)
		}
		c.mu.Unlock()
	}()

	buf := make([]byte, 512)
	var currentStream uint32

	for {
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := c.conn.Read(buf)
		if err != nil {
			if c.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				c.mu.Lock()
				c.missedPings++
				missed := c.missedPings
				c.mu.Unlock()
				if missed >= 3 {
					log.Println("[DMR] Too many missed pings, disconnecting")
					return
				}
				continue
			}
			log.Printf("[DMR] Read error: %v", err)
			return
		}

		data := buf[:n]

		// MSTPING — respond with RPTPONG
		if n >= 7 && string(data[:7]) == SigMSTP {
			c.mu.Lock()
			c.missedPings = 0
			c.mu.Unlock()
			c.conn.Write(BuildPongPacket(c.rptID))
			continue
		}

		// MSTCL — master close
		if n >= 5 && string(data[:5]) == SigMSTC {
			log.Println("[DMR] Master sent close")
			return
		}

		// DMRD — voice/data frame
		if n >= DMRDFrameSize && string(data[:4]) == SigDMRD {
			frame := ParseDMRDFrame(data)
			if frame == nil {
				continue
			}

			// Filter by our talkgroup and timeslot
			if frame.DstID != c.talkgroup {
				continue
			}
			if frame.Slot != c.timeslot {
				continue
			}
			// Skip our own transmissions
			if frame.RptID == c.rptID {
				continue
			}

			// Voice LC Header — new call
			if frame.IsHeader() {
				if frame.StreamID != currentStream {
					currentStream = frame.StreamID
					log.Printf("[DMR] Call start: src=%d dst=%d stream=%08X",
						frame.SrcID, frame.DstID, frame.StreamID)
					if c.onCallStart != nil {
						c.onCallStart(frame.SrcID, frame.DstID)
					}
				}
				continue
			}

			// Voice Terminator
			if frame.IsTerminator() {
				log.Printf("[DMR] Call end: src=%d stream=%08X", frame.SrcID, frame.StreamID)
				if c.onCallEnd != nil {
					c.onCallEnd(frame.SrcID)
				}
				currentStream = 0
				continue
			}

			// Voice frame — extract 3 AMBE frames
			if frame.IsVoice() {
				ambeFrames := ExtractAMBE(frame.Payload)
				if c.onVoice != nil {
					c.onVoice(frame.SrcID, ambeFrames)
				}
			}
		}
	}
}

// StartTX begins a new voice transmission to the DMR network.
func (c *DMRClient) StartTX() {
	c.txMu.Lock()
	defer c.txMu.Unlock()

	c.txStreamID = rand.Uint32()
	c.txSeq = 0
	c.txBurst = 0
	c.txBuf = c.txBuf[:0]

	log.Printf("[DMR] TX start: stream=%08X TG=%d TS=%d", c.txStreamID, c.talkgroup, c.timeslot+1)

	// Send Voice LC Header
	header := BuildVoiceLCHeader(c.txSeq, c.rptID, c.talkgroup, c.rptID,
		c.txStreamID, c.timeslot, CallTypeGroup)
	c.conn.Write(header)
	c.txSeq++
}

// SendVoice buffers an AMBE frame and sends when 3 frames are accumulated.
func (c *DMRClient) SendVoice(ambe [9]byte) error {
	c.txMu.Lock()
	defer c.txMu.Unlock()

	c.txBuf = append(c.txBuf, ambe)

	// Need 3 frames per DMRD packet
	if len(c.txBuf) < 3 {
		return nil
	}

	var frames [3][9]byte
	copy(frames[0][:], c.txBuf[0][:])
	copy(frames[1][:], c.txBuf[1][:])
	copy(frames[2][:], c.txBuf[2][:])
	c.txBuf = c.txBuf[:0]

	payload := BuildAMBEPayload(frames, c.txBurst)

	// Determine frame type
	var frameType byte
	if c.txBurst == 0 {
		frameType = FrameTypeVoiceSync
	} else {
		frameType = FrameTypeVoice
	}

	pkt := BuildDMRDFrame(c.txSeq, c.rptID, c.talkgroup, c.rptID,
		c.timeslot, CallTypeGroup, frameType, byte(c.txBurst), c.txStreamID, payload)

	_, err := c.conn.Write(pkt)
	c.txSeq++
	c.txBurst = (c.txBurst + 1) % 6

	return err
}

// StopTX ends the current voice transmission.
func (c *DMRClient) StopTX() error {
	c.txMu.Lock()
	defer c.txMu.Unlock()

	// Flush remaining frames with silence padding
	for len(c.txBuf) > 0 && len(c.txBuf) < 3 {
		c.txBuf = append(c.txBuf, AMBESilence)
	}
	if len(c.txBuf) == 3 {
		var frames [3][9]byte
		copy(frames[0][:], c.txBuf[0][:])
		copy(frames[1][:], c.txBuf[1][:])
		copy(frames[2][:], c.txBuf[2][:])
		c.txBuf = c.txBuf[:0]

		payload := BuildAMBEPayload(frames, c.txBurst)
		var frameType byte
		if c.txBurst == 0 {
			frameType = FrameTypeVoiceSync
		} else {
			frameType = FrameTypeVoice
		}
		pkt := BuildDMRDFrame(c.txSeq, c.rptID, c.talkgroup, c.rptID,
			c.timeslot, CallTypeGroup, frameType, byte(c.txBurst), c.txStreamID, payload)
		c.conn.Write(pkt)
		c.txSeq++
	}

	// Send Voice Terminator
	term := BuildVoiceTerminator(c.txSeq, c.rptID, c.talkgroup, c.rptID,
		c.txStreamID, c.timeslot, CallTypeGroup)
	_, err := c.conn.Write(term)

	log.Printf("[DMR] TX stop: stream=%08X", c.txStreamID)
	return err
}

func (c *DMRClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *DMRClient) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
