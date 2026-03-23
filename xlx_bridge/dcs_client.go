package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// DCSClient manages the DCS protocol connection to an XLX reflector.
type DCSClient struct {
	host          string
	port          int
	callsign      string // 8-char DCS callsign for connection
	mycall        string // MY CALL for D-STAR voice frames (up to 8 chars)
	mycallSuffix  string // MY CALL suffix (up to 4 chars, e.g. "AMBE")
	clientModule  byte   // client module derived from DCS callsign suffix
	module        byte   // 'A'-'Z' target reflector module
	reflectorName string // e.g. "XLX585"

	conn *net.UDPConn

	// TX state
	txStreamID uint16
	txSeq      uint32
	txFrameID  int
	txMycall   string           // dynamic MYCALL for current TX (originating callsign)
	txSlowData *SlowDataEncoder // slow data text for current TX

	// Callbacks
	onVoiceFrame func(frame *DCSVoiceFrame)

	// Diagnostic: last RX stream ID logged (to log first frame of each stream)
	lastRxLogStream uint16

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewDCSClient(host string, port int, callsign string, module byte, reflectorName string, mycall string, mycallSuffix string) *DCSClient {
	if mycallSuffix == "" {
		mycallSuffix = "AMBE"
	}
	// Client module is the last character of the 8-char DCS callsign
	clientModule := byte('A')
	if len(callsign) >= 8 {
		clientModule = callsign[7]
	} else if len(callsign) > 0 {
		clientModule = callsign[len(callsign)-1]
	}
	return &DCSClient{
		host:          host,
		port:          port,
		callsign:      callsign,
		mycall:        mycall,
		mycallSuffix:  mycallSuffix,
		clientModule:  clientModule,
		module:        module,
		reflectorName: reflectorName,
	}
}

func (c *DCSClient) SetVoiceCallback(cb func(frame *DCSVoiceFrame)) {
	c.onVoiceFrame = cb
}

// Done returns a channel that is closed when the connection drops.
func (c *DCSClient) Done() <-chan struct{} {
	return c.done
}

// Connect performs the DCS handshake with the XLX reflector.
func (c *DCSClient) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	log.Printf("[DCS] Connecting to %s module %c...", addr, c.module)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial: %w", err)
	}
	c.conn = conn

	// Send disconnect first to clean up any stale session
	c.conn.Write(BuildDCSDisconnect(c.callsign, c.clientModule))
	time.Sleep(100 * time.Millisecond)

	// Send connect packet
	connectPkt := BuildDCSConnect(c.callsign, c.clientModule, c.module)
	log.Printf("[DCS] Connect packet first 16 bytes: % X", connectPkt[:16])
	if _, err := c.conn.Write(connectPkt); err != nil {
		return fmt.Errorf("send connect: %w", err)
	}

	// Wait for ACK
	resp, err := c.readPacket(5 * time.Second)
	if err != nil {
		return fmt.Errorf("read connect response (no ACK from XLX): %w", err)
	}
	log.Printf("[DCS] Response (%d bytes): % X", len(resp), resp[:min(len(resp), 20)])

	if len(resp) >= 14 && string(resp[10:13]) == "ACK" {
		log.Printf("[DCS] Connected to %s module %c", c.reflectorName, c.module)
		return nil
	}

	if len(resp) >= 14 && string(resp[10:13]) == "NAK" {
		return fmt.Errorf("connection rejected (NAK)")
	}

	return fmt.Errorf("unexpected response (%d bytes): %q", len(resp), resp[:min(len(resp), 20)])
}

// SetTXOrigin sets the originating callsign for the next transmission.
// This updates MYCALL in the DCS header and generates a slow data text
// message (e.g. "Via VE3ABC") visible on D-STAR radios.
func (c *DCSClient) SetTXOrigin(callsign string) {
	cs := strings.TrimSpace(callsign)
	text := "Via " + cs
	if len(text) > 20 {
		text = text[:20]
	}

	c.mu.Lock()
	c.txMycall = cs
	c.txSlowData = NewSlowDataText(text)
	c.mu.Unlock()
	log.Printf("[DCS] TX origin set: MYCALL=%s slow_data=%q", cs, text)
}

// StartTX begins a new voice transmission.
func (c *DCSClient) StartTX() {
	c.mu.Lock()
	c.txStreamID = newStreamID16()
	c.txSeq = 0
	c.txFrameID = 0
	c.mu.Unlock()
	log.Printf("[DCS] TX started (stream %04X)", c.txStreamID)
}

// TXStreamID returns the current outgoing stream ID (used to filter self-echo).
func (c *DCSClient) TXStreamID() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.txStreamID
}

// rptReflector returns the 8-char RPT2 field (destination): reflector name + module.
// e.g. "DCS585" + 'A' → "DCS585 A"
func (c *DCSClient) rptReflector() string {
	return string(padCallsign(c.reflectorName, 7)) + string(c.module)
}

// rptGateway returns the 8-char RPT1 field (source gateway): client callsign.
// e.g. "SVX585 A"
func (c *DCSClient) rptGateway() string {
	return string(padCallsign(c.callsign, 8))
}

// SendVoice sends a single AMBE voice frame.
// Returns nil without sending if no TX session is active (StartTX not called).
func (c *DCSClient) SendVoice(ambe [9]byte) error {
	c.mu.Lock()
	streamID := c.txStreamID
	if streamID == 0 {
		c.mu.Unlock()
		return nil // no active TX — drop frame (audio arrived before StartTX)
	}
	seq := c.txSeq
	c.txSeq++
	frameID := c.txFrameID
	c.txFrameID = (c.txFrameID + 1) % DCSFramesPerSuperframe
	slowDataEnc := c.txSlowData
	mycall := c.txMycall
	c.mu.Unlock()

	// Use dynamic MYCALL if set, otherwise fall back to configured default
	if mycall == "" {
		mycall = c.mycall
	}

	// Build slow data from encoder, or use defaults
	var slowData [3]byte
	if slowDataEnc != nil {
		slowData = slowDataEnc.Frame(frameID)
	} else if frameID == 0 {
		slowData = dvSlowDataSync
	}

	rpt2 := c.rptReflector() // destination: reflector module
	rpt1 := c.rptGateway()   // source: our client callsign

	pkt := BuildDCSVoice(rpt2, rpt1, "CQCQCQ", mycall, c.mycallSuffix, streamID, byte(frameID), ambe, slowData, seq)
	if seq == 0 {
		log.Printf("[DCS] TX pkt[0:62]:  % X", pkt[:62])
		log.Printf("[DCS] TX pkt[62:100]: % X", pkt[62:])
	}
	_, err := c.conn.Write(pkt)
	return err
}

// StopTX ends the current voice transmission by sending a last-frame marker.
func (c *DCSClient) StopTX() error {
	c.mu.Lock()
	streamID := c.txStreamID
	seq := c.txSeq
	c.txSeq++
	mycall := c.txMycall
	// Clear TX state for next transmission
	c.txMycall = ""
	c.txSlowData = nil
	c.txStreamID = 0 // mark TX inactive so SendVoice won't send stale frames
	c.mu.Unlock()

	if mycall == "" {
		mycall = c.mycall
	}

	rpt2 := c.rptReflector()
	rpt1 := c.rptGateway()

	// Last frame: packetID has bit 6 set, AMBE is silence
	packetID := byte(0x40) // last frame flag
	pkt := BuildDCSVoice(rpt2, rpt1, "CQCQCQ", mycall, c.mycallSuffix, streamID, packetID, dstarSilenceAMBE, [3]byte{}, seq)
	_, err := c.conn.Write(pkt)
	if err != nil {
		return err
	}
	log.Printf("[DCS] TX stopped (stream %04X)", streamID)
	return nil
}

// RunReader reads incoming packets and dispatches voice frames.
func (c *DCSClient) RunReader() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.done)
		}
		c.mu.Unlock()
	}()

	missedPings := 0
	rxVoiceLogged := false
	echoTestDone := false
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
					log.Println("[DCS] No response from reflector, disconnecting")
					return
				}
				continue
			}
			log.Printf("[DCS] Read error: %v", err)
			return
		}
		missedPings = 0

		data := buf[:n]

		// Log packet types for debugging
		if n < DCSVoiceSize || string(data[0:4]) != "0001" {
			log.Printf("[DCS] RX packet (%d bytes): % X", n, data[:min(n, 20)])
		}

		// Voice frame (100 bytes, starts with "0001")
		if n >= DCSVoiceSize && string(data[0:4]) == "0001" {
			if !rxVoiceLogged {
				rxVoiceLogged = true
				log.Printf("[DCS] RX raw voice pkt[0:62]: % X", data[:62])
				log.Printf("[DCS] RX raw voice pkt[62:100]: % X", data[62:100])
			}

			// Echo test: send back 10 received frames with a new stream ID
			// to verify XLX will distribute packets from our UDP socket
			if !echoTestDone {
				echo := make([]byte, n)
				copy(echo, data[:n])
				// Replace stream ID with a random one
				sid := newStreamID16()
				echo[43] = byte(sid >> 8)
				echo[44] = byte(sid)
				nn, werr := c.conn.Write(echo)
				log.Printf("[DCS] ECHO TEST: sent %d/%d bytes (stream %04X), err=%v", nn, len(echo), sid, werr)
				if echo[45]&0x40 != 0 { // last frame
					echoTestDone = true
					log.Println("[DCS] ECHO TEST complete (last frame sent)")
				}
			}

			frame := ParseDCSVoice(data)
			if frame == nil {
				continue
			}

			// Skip our own transmissions (by callsign or by stream ID echo)
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

		// Keepalive from server — we don't need to respond, just acknowledge receipt
		// ACK/NAK responses (14 bytes)
		// Other packets — ignore
	}
}

// RunKeepalive sends periodic ping packets.
func (c *DCSClient) RunKeepalive() {
	ticker := time.NewTicker(time.Duration(DCSKeepaliveInterval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if c.isClosed() {
			return
		}
		pkt := BuildDCSKeepalive(c.callsign, c.clientModule, c.reflectorName)
		if _, err := c.conn.Write(pkt); err != nil {
			log.Printf("[DCS] Keepalive error: %v", err)
			return
		}
	}
}

func (c *DCSClient) readPacket(timeout time.Duration) ([]byte, error) {
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (c *DCSClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *DCSClient) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	c.mu.Unlock()

	if c.conn != nil {
		c.conn.Write(BuildDCSDisconnect(c.callsign, c.clientModule))
		c.conn.Close()
	}
}

func newStreamID16() uint16 {
	var b [2]byte
	rand.Read(b[:])
	return uint16(b[0])<<8 | uint16(b[1])
}
