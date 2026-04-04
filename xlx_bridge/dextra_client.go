package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// DExtraClient manages a DExtra protocol connection to an XLX reflector.
type DExtraClient struct {
	host          string
	port          int
	callsign      string // 8-char DExtra callsign
	mycall        string // MY CALL for D-STAR voice frames
	mycallSuffix  string // MY CALL suffix (4 chars)
	clientModule  byte
	module        byte   // target reflector module
	reflectorName string

	conn *net.UDPConn

	// TX state
	txStreamID   uint16
	txSeq        uint32
	txFrameID    int
	txMycall     string
	txSlowData   *SlowDataEncoder
	txHeaderSent bool // whether DSVT header has been sent for current TX

	// RX state: cache header info keyed by stream ID
	rxHeaders map[uint16]*rxHeaderInfo

	// Callbacks
	onVoiceFrame func(frame *DCSVoiceFrame)

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

type rxHeaderInfo struct {
	RPT1     string
	RPT2     string
	URCall   string
	MYCall   string
	MYSuffix string
	lastSeen time.Time
}

func NewDExtraClient(host string, port int, callsign string, module byte, reflectorName string, mycall string, mycallSuffix string) *DExtraClient {
	if mycallSuffix == "" {
		mycallSuffix = "AMBE"
	}
	clientModule := byte('A')
	if len(callsign) >= 8 {
		clientModule = callsign[7]
	} else if len(callsign) > 0 {
		clientModule = callsign[len(callsign)-1]
	}
	return &DExtraClient{
		host:          host,
		port:          port,
		callsign:      callsign,
		mycall:        mycall,
		mycallSuffix:  mycallSuffix,
		clientModule:  clientModule,
		module:        module,
		reflectorName: reflectorName,
		rxHeaders:     make(map[uint16]*rxHeaderInfo),
	}
}

func (c *DExtraClient) SetVoiceCallback(cb func(frame *DCSVoiceFrame)) {
	c.onVoiceFrame = cb
}

func (c *DExtraClient) Done() <-chan struct{} {
	return c.done
}

func (c *DExtraClient) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.mu.Unlock()

	addr := net.JoinHostPort(c.host, fmt.Sprintf("%d", c.port))
	log.Printf("[DExtra] Connecting to %s module %c...", addr, c.module)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial: %w", err)
	}
	c.conn = conn

	// Send disconnect first to clean up stale sessions
	c.conn.Write(BuildDExtraDisconnect(c.callsign, c.clientModule))
	time.Sleep(100 * time.Millisecond)

	// Send connect
	connectPkt := BuildDExtraConnect(c.callsign, c.clientModule, c.module)
	if _, err := c.conn.Write(connectPkt); err != nil {
		return fmt.Errorf("send connect: %w", err)
	}

	// Wait for ACK — DExtra ACK can be various formats:
	// - 11 bytes: reflector(8) + module + clientModule + \0 (XRF style)
	// - contains "ACK" somewhere (DExtra style)
	// - 11 bytes with byte[9] != space (connected indicator)
	resp, err := c.readPacket(5 * time.Second)
	if err != nil {
		return fmt.Errorf("read connect response: %w", err)
	}
	log.Printf("[DExtra] Response (%d bytes): % X", len(resp), resp)

	// Check for NAK first
	if strings.Contains(string(resp), "NAK") {
		return fmt.Errorf("connection rejected (NAK)")
	}

	// XRF-style ACK: 11 bytes with byte[9] != space (means connected)
	if len(resp) >= 11 && resp[9] != ' ' {
		log.Printf("[DExtra] Connected to %s module %c", c.reflectorName, c.module)
		return nil
	}

	// DExtra-style ACK: contains "ACK"
	if strings.Contains(string(resp), "ACK") {
		log.Printf("[DExtra] Connected to %s module %c", c.reflectorName, c.module)
		return nil
	}

	return fmt.Errorf("unexpected response (%d bytes): %q", len(resp), resp)
}

// SetTXOrigin sets the originating callsign for the next transmission.
func (c *DExtraClient) SetTXOrigin(callsign string) {
	cs := strings.TrimSpace(callsign)
	text := "Via " + cs
	if len(text) > 20 {
		text = text[:20]
	}
	c.mu.Lock()
	c.txMycall = cs
	c.txSlowData = NewSlowDataText(text)
	c.mu.Unlock()
	log.Printf("[DExtra] TX origin set: MYCALL=%s slow_data=%q", cs, text)
}

func (c *DExtraClient) StartTX() {
	c.mu.Lock()
	c.txStreamID = newStreamID16()
	c.txSeq = 0
	c.txFrameID = 0
	c.txHeaderSent = false
	c.mu.Unlock()
	log.Printf("[DExtra] TX started (stream %04X)", c.txStreamID)
}

func (c *DExtraClient) TXStreamID() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.txStreamID
}

// rptReflector returns the RPT2 field (destination): XRF reflector name + module.
// DExtra uses XRF prefix (e.g. "XRF585 A") regardless of configured reflector name.
func (c *DExtraClient) rptReflector() string {
	// Derive XRF name from configured name: DCS585 → XRF585, XLX585 → XRF585
	name := c.reflectorName
	if len(name) >= 3 {
		name = "XRF" + name[3:]
	}
	return string(padCallsign(name, 7)) + string(c.module)
}

// rptGateway returns the RPT1 field (source gateway): client callsign.
func (c *DExtraClient) rptGateway() string {
	return string(padCallsign(c.callsign, 8))
}

func (c *DExtraClient) SendVoice(ambe [9]byte) error {
	c.mu.Lock()
	streamID := c.txStreamID
	if streamID == 0 {
		c.mu.Unlock()
		return nil
	}
	headerNeeded := !c.txHeaderSent
	if headerNeeded {
		c.txHeaderSent = true
	}
	seq := c.txSeq
	c.txSeq++
	frameID := c.txFrameID
	c.txFrameID = (c.txFrameID + 1) % DCSFramesPerSuperframe
	slowDataEnc := c.txSlowData
	mycall := c.txMycall
	c.mu.Unlock()

	if mycall == "" {
		mycall = c.mycall
	}

	// Send DSVT header on first frame of TX
	if headerNeeded {
		rpt2 := c.rptReflector()
		rpt1 := c.rptGateway()
		hdr := BuildDExtraVoiceHeader(rpt2, rpt1, "CQCQCQ", mycall, c.mycallSuffix, streamID)
		if _, err := c.conn.Write(hdr); err != nil {
			return fmt.Errorf("send voice header: %w", err)
		}
		log.Printf("[DExtra] TX header: RPT2=%q RPT1=%q MY=%q/%q stream=%04X (%d bytes)",
			rpt2, rpt1, mycall, c.mycallSuffix, streamID, len(hdr))
	}

	// Build slow data
	var slowData [3]byte
	if slowDataEnc != nil {
		slowData = slowDataEnc.Frame(frameID)
	} else if frameID == 0 {
		slowData = dvSlowDataSync
	}

	pkt := BuildDExtraVoiceFrame(streamID, byte(frameID), ambe, slowData)
	if seq == 0 {
		log.Printf("[DExtra] TX first voice frame: stream=%04X pkt: % X", streamID, pkt)
	}
	_, err := c.conn.Write(pkt)
	return err
}

func (c *DExtraClient) StopTX() error {
	c.mu.Lock()
	streamID := c.txStreamID
	seq := c.txSeq
	c.txSeq++
	mycall := c.txMycall
	c.txMycall = ""
	c.txSlowData = nil
	c.txStreamID = 0
	c.mu.Unlock()

	if mycall == "" {
		mycall = c.mycall
	}

	// If header was never sent (no audio frames), send it now
	// so the stream is properly opened before we close it
	if streamID == 0 {
		log.Printf("[DExtra] TX stop skipped (no active stream)")
		return nil
	}

	_ = mycall
	_ = seq

	// Send last frame
	pkt := BuildDExtraVoiceFrame(streamID, 0x40, dstarSilenceAMBE, [3]byte{})
	_, err := c.conn.Write(pkt)
	if err != nil {
		return err
	}
	log.Printf("[DExtra] TX stopped (stream %04X)", streamID)
	return nil
}

func (c *DExtraClient) RunReader() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.done)
		}
		c.mu.Unlock()
	}()

	missedPings := 0
	buf := make([]byte, 1024)
	for {
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := c.conn.Read(buf)
		if err != nil {
			if c.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				missedPings++
				if missedPings >= 3 {
					log.Println("[DExtra] No response from reflector, disconnecting")
					return
				}
				continue
			}
			log.Printf("[DExtra] Read error: %v", err)
			return
		}
		missedPings = 0
		data := buf[:n]

		// DSVT voice header (56 bytes)
		if n == DExtraVoiceHeaderSize && string(data[0:4]) == "DSVT" && data[4] == 0x10 {
			log.Printf("[DExtra] RX header raw[0:14]: % X", data[:14])
			log.Printf("[DExtra] RX header raw[14:56]: % X", data[14:56])
			streamID, rpt2, rpt1, _, myCall, mySuffix := ParseDExtraVoiceHeader(data)
			c.mu.Lock()
			c.rxHeaders[streamID] = &rxHeaderInfo{
				RPT1: rpt1, RPT2: rpt2,
				MYCall: myCall, MYSuffix: mySuffix,
				lastSeen: time.Now(),
			}
			// Evict old entries
			if len(c.rxHeaders) > 20 {
				for k, v := range c.rxHeaders {
					if time.Since(v.lastSeen) > 30*time.Second {
						delete(c.rxHeaders, k)
					}
				}
			}
			c.mu.Unlock()
			continue
		}

		// DSVT voice frame (27 bytes)
		if n == DExtraVoiceFrameSize && string(data[0:4]) == "DSVT" && data[4] == 0x20 {
			if missedPings == 0 { // log first voice frame after keepalive reset
				log.Printf("[DExtra] RX voice raw: % X", data[:27])
				missedPings = -1 // only log once
			}
			frame := ParseDExtraVoiceFrame(data)
			if frame == nil {
				continue
			}

			// Populate header fields from cache
			c.mu.Lock()
			if hdr, ok := c.rxHeaders[frame.StreamID]; ok {
				frame.RPT1 = hdr.RPT1
				frame.RPT2 = hdr.RPT2
				frame.MYCall = hdr.MYCall
				frame.MYSuffix = hdr.MYSuffix
				hdr.lastSeen = time.Now()
			}
			c.mu.Unlock()

			// Skip our own transmissions
			myCS := strings.TrimSpace(c.callsign)
			srcCS := strings.TrimSpace(frame.MYCall)
			if strings.EqualFold(myCS, srcCS) {
				continue
			}
			if txSID := c.TXStreamID(); txSID != 0 && frame.StreamID == txSID {
				continue
			}

			if c.onVoiceFrame != nil {
				c.onVoiceFrame(frame)
			}
			continue
		}

		// Keepalive or other small packets
		if n < DExtraVoiceFrameSize {
			// ACK/NAK or keepalive — nothing to do
			continue
		}

		// Unknown packet
		if n > DExtraKeepaliveSize {
			log.Printf("[DExtra] RX unknown packet (%d bytes): % X", n, data[:min(n, 20)])
		}
	}
}

func (c *DExtraClient) RunKeepalive() {
	ticker := time.NewTicker(time.Duration(DExtraKeepaliveInterval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if c.isClosed() {
			return
		}
		pkt := BuildDExtraKeepalive(c.callsign)
		if _, err := c.conn.Write(pkt); err != nil {
			log.Printf("[DExtra] Keepalive error: %v", err)
			return
		}
	}
}

func (c *DExtraClient) readPacket(timeout time.Duration) ([]byte, error) {
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (c *DExtraClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *DExtraClient) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	c.mu.Unlock()

	if c.conn != nil {
		c.conn.Write(BuildDExtraDisconnect(c.callsign, c.clientModule))
		c.conn.Close()
	}
}
