package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// ODMRTPClient connects to a BrandMeister Open DMR Terminal (REWIND) server.
// It implements the same dmrConn interface as DMRClient so the audio pipeline
// in runBridge is identical regardless of the DMR transport.
type ODMRTPClient struct {
	host      string
	port      int
	dmrID     uint32
	password  string
	callsign  string
	talkgroup uint32

	conn *net.UDPConn

	// Writes (keep-alive vs. TX) come from different goroutines.
	writeMu sync.Mutex
	// routineSeq numbers control packets; realtimeSeq numbers DMR data packets.
	// Each is only advanced from a single goroutine (handshake/reader vs. TX).
	routineSeq  uint32
	realtimeSeq uint32

	// TX state — accessed only under txMu (the SVX audio goroutine).
	txMu  sync.Mutex
	txBuf [][9]byte

	// RX call state — accessed only from RunReader.
	rxSrcID  uint32
	rxActive bool

	onVoice     func(srcID uint32, frames [3][9]byte)
	onCallStart func(srcID, dstID uint32)
	onCallEnd   func(srcID uint32)

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewODMRTPClient(host string, port int, dmrID uint32, password, callsign string, talkgroup uint32) *ODMRTPClient {
	return &ODMRTPClient{
		host:      host,
		port:      port,
		dmrID:     dmrID,
		password:  password,
		callsign:  callsign,
		talkgroup: talkgroup,
	}
}

func (c *ODMRTPClient) Done() <-chan struct{}                        { return c.done }
func (c *ODMRTPClient) SetVoiceCallback(cb func(uint32, [3][9]byte)) { c.onVoice = cb }
func (c *ODMRTPClient) SetCallStartCallback(cb func(uint32, uint32)) { c.onCallStart = cb }
func (c *ODMRTPClient) SetCallEndCallback(cb func(uint32))           { c.onCallEnd = cb }

// send writes a fully-framed packet, serialized against concurrent senders.
func (c *ODMRTPClient) send(pkt []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.conn.Write(pkt)
	return err
}

func (c *ODMRTPClient) sendRoutine(msgType uint16, payload []byte) error {
	pkt := buildRewind(msgType, rewindFlagNone, c.routineSeq, payload)
	c.routineSeq++
	return c.send(pkt)
}

func (c *ODMRTPClient) sendRealtime(msgType uint16, payload []byte) error {
	pkt := buildRewind(msgType, rewindFlagRealTime1, c.realtimeSeq, payload)
	c.realtimeSeq++
	return c.send(pkt)
}

func (c *ODMRTPClient) sendKeepAlive() error {
	return c.sendRoutine(rewindTypeKeepAlive, buildVersionData(c.dmrID))
}

// Connect dials the server and drives the REWIND login handshake to completion:
// keep-alive -> challenge -> authentication -> configuration -> subscription.
func (c *ODMRTPClient) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.routineSeq = 0
	c.realtimeSeq = 0
	c.mu.Unlock()

	addr := net.JoinHostPort(c.host, fmt.Sprintf("%d", c.port))
	log.Printf("[ODMRTP] Connecting to terminal server at %s...", addr)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial: %w", err)
	}
	c.conn = conn

	// Solicit a challenge by announcing ourselves.
	if err := c.sendKeepAlive(); err != nil {
		return fmt.Errorf("send keepalive: %w", err)
	}

	buf := make([]byte, 512)
	authSent := false
	provisioned := false
	deadline := time.Now().Add(15 * time.Second)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("login timeout")
		}
		c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := c.conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				c.sendKeepAlive() // nudge and keep waiting
				continue
			}
			return fmt.Errorf("handshake read: %w", err)
		}
		pkt, ok := parseRewind(buf[:n])
		if !ok {
			continue
		}
		switch pkt.Type {
		case rewindTypeChallenge:
			log.Printf("[ODMRTP] Challenge received (%d-byte salt)", len(pkt.Payload))
			if err := c.sendRoutine(rewindTypeAuthentication, authResponse(pkt.Payload, c.password)); err != nil {
				return fmt.Errorf("send auth: %w", err)
			}
			authSent = true
		case rewindTypeKeepAlive:
			// An empty keep-alive after auth means we were accepted: request
			// super headers, then subscribe to our talkgroup.
			if authSent && !provisioned {
				provisioned = true
				c.sendRoutine(rewindTypeConfiguration, buildConfigData(rewindOptionSuperHeader))
				c.sendRoutine(rewindTypeSubscription, buildSubscriptionData(rewindSessionGroupVoice, c.talkgroup))
			}
		case rewindTypeSubscription:
			log.Printf("[ODMRTP] Logged in, subscribed to TG %d", c.talkgroup)
			return nil
		case rewindTypeClose:
			return fmt.Errorf("server sent close during login")
		case rewindTypeFailureCode:
			return fmt.Errorf("server sent failure code during login")
		}
	}
}

// RunReader is the steady-state loop: keep-alive heartbeat, server-timeout
// watchdog, RX call assembly, and re-auth on challenge.
func (c *ODMRTPClient) RunReader() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.done)
		}
		c.mu.Unlock()
	}()

	buf := make([]byte, 512)
	lastKA := time.Now()
	lastRecv := time.Now()
	lastAudio := time.Now()

	for {
		if c.isClosed() {
			return
		}
		if time.Since(lastKA) >= rewindKeepAlive*time.Second {
			c.sendKeepAlive()
			lastKA = time.Now()
		}
		if time.Since(lastRecv) > 30*time.Second {
			log.Println("[ODMRTP] Server timeout, disconnecting")
			return
		}
		// End a call whose terminator was lost.
		if c.rxActive && time.Since(lastAudio) > 2*time.Second {
			c.endCall()
		}

		c.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := c.conn.Read(buf)
		if err != nil {
			if c.isClosed() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("[ODMRTP] Read error: %v", err)
			return
		}
		lastRecv = time.Now()

		pkt, ok := parseRewind(buf[:n])
		if !ok {
			continue
		}
		switch pkt.Type {
		case rewindTypeKeepAlive:
			// heartbeat ack

		case rewindTypeSuperHeader:
			_, srcID, dstID, ok := parseSuperHeader(pkt.Payload)
			if !ok || dstID != c.talkgroup {
				continue
			}
			if c.rxActive && srcID != c.rxSrcID {
				c.endCall()
			}
			if !c.rxActive {
				c.rxActive = true
				c.rxSrcID = srcID
				log.Printf("[ODMRTP] Call start: src=%d dst=%d", srcID, dstID)
				if c.onCallStart != nil {
					c.onCallStart(srcID, dstID)
				}
			}
			lastAudio = time.Now()

		case rewindTypeDMRAudioFrame:
			if len(pkt.Payload) < odmrtpAudioFrameLen {
				continue
			}
			lastAudio = time.Now()
			// Audio may arrive before a super header (or with none); start a
			// call implicitly so audio is never dropped.
			if !c.rxActive {
				c.rxActive = true
				log.Printf("[ODMRTP] Call start (implicit): src=%d", c.rxSrcID)
				if c.onCallStart != nil {
					c.onCallStart(c.rxSrcID, c.talkgroup)
				}
			}
			var frames [3][9]byte
			copy(frames[0][:], pkt.Payload[0:9])
			copy(frames[1][:], pkt.Payload[9:18])
			copy(frames[2][:], pkt.Payload[18:27])
			if c.onVoice != nil {
				c.onVoice(c.rxSrcID, frames)
			}

		case rewindTypeDMRTerminator:
			c.endCall()

		case rewindTypeChallenge:
			// Server re-challenged (e.g. session refresh) — re-authenticate.
			c.sendRoutine(rewindTypeAuthentication, authResponse(pkt.Payload, c.password))

		case rewindTypeClose:
			log.Println("[ODMRTP] Server sent close")
			return
		}
	}
}

func (c *ODMRTPClient) endCall() {
	if !c.rxActive {
		return
	}
	src := c.rxSrcID
	c.rxActive = false
	c.rxSrcID = 0
	log.Printf("[ODMRTP] Call end: src=%d", src)
	if c.onCallEnd != nil {
		c.onCallEnd(src)
	}
}

// StartTX announces a new outbound call with a super header.
func (c *ODMRTPClient) StartTX() {
	c.txMu.Lock()
	defer c.txMu.Unlock()

	c.txBuf = c.txBuf[:0]
	log.Printf("[ODMRTP] TX start: TG=%d src=%d", c.talkgroup, c.dmrID)
	sh := buildSuperHeader(rewindSessionGroupVoice, c.dmrID, c.talkgroup, c.callsign)
	c.sendRealtime(rewindTypeSuperHeader, sh)
}

// SendVoice buffers an AMBE frame and emits an audio packet every 3 frames.
func (c *ODMRTPClient) SendVoice(ambe [9]byte) error {
	c.txMu.Lock()
	defer c.txMu.Unlock()

	c.txBuf = append(c.txBuf, ambe)
	if len(c.txBuf) < 3 {
		return nil
	}
	payload := c.packAudio()
	return c.sendRealtime(rewindTypeDMRAudioFrame, payload)
}

// StopTX flushes any partial superframe (padding with silence) and terminates.
func (c *ODMRTPClient) StopTX() error {
	c.txMu.Lock()
	defer c.txMu.Unlock()

	for len(c.txBuf) > 0 && len(c.txBuf) < 3 {
		c.txBuf = append(c.txBuf, AMBESilence)
	}
	if len(c.txBuf) == 3 {
		c.sendRealtime(rewindTypeDMRAudioFrame, c.packAudio())
	}
	log.Printf("[ODMRTP] TX stop")
	return c.sendRealtime(rewindTypeDMRTerminator, nil)
}

// packAudio consumes 3 buffered AMBE frames into a 27-byte mode-33 payload.
// Caller must hold txMu and ensure len(txBuf) >= 3.
func (c *ODMRTPClient) packAudio() []byte {
	payload := make([]byte, odmrtpAudioFrameLen)
	copy(payload[0:9], c.txBuf[0][:])
	copy(payload[9:18], c.txBuf[1][:])
	copy(payload[18:27], c.txBuf[2][:])
	c.txBuf = c.txBuf[:0]
	return payload
}

func (c *ODMRTPClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *ODMRTPClient) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	c.mu.Unlock()

	if c.conn != nil {
		c.send(buildRewind(rewindTypeClose, rewindFlagNone, c.routineSeq, nil))
		c.conn.Close()
	}
}
