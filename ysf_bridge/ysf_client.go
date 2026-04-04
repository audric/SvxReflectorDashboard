package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// YSFClient manages the connection to a YSF reflector.
type YSFClient struct {
	host     string
	port     int
	callsign string

	conn *net.UDPConn

	// TX state
	txCounter byte
	txFN      byte

	// Callbacks
	onVoiceFrame func(frame *YSFVoiceFrame)

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewYSFClient(host string, port int, callsign string) *YSFClient {
	return &YSFClient{
		host:     host,
		port:     port,
		callsign: callsign,
	}
}

func (c *YSFClient) SetVoiceCallback(cb func(frame *YSFVoiceFrame)) {
	c.onVoiceFrame = cb
}

func (c *YSFClient) Done() <-chan struct{} {
	return c.done
}

// Connect registers with the YSF reflector.
func (c *YSFClient) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.mu.Unlock()

	addr := net.JoinHostPort(c.host, fmt.Sprintf("%d", c.port))
	log.Printf("[YSF] Connecting to %s...", addr)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial: %w", err)
	}
	c.conn = conn

	// Send disconnect first to clean up stale session
	c.conn.Write(BuildYSFUnlink(c.callsign))
	time.Sleep(100 * time.Millisecond)

	// Send poll to register
	if _, err := c.conn.Write(BuildYSFPoll(c.callsign)); err != nil {
		return fmt.Errorf("send poll: %w", err)
	}

	// Wait for YSFP response
	resp, err := c.readPacket(5 * time.Second)
	if err != nil {
		return fmt.Errorf("no response from reflector: %w", err)
	}

	if len(resp) >= 4 && string(resp[0:4]) == "YSFP" {
		name := strings.TrimSpace(string(resp[4:]))
		log.Printf("[YSF] Connected to %s", name)
		return nil
	}

	return fmt.Errorf("unexpected response (%d bytes): %q", len(resp), resp[:min(len(resp), 20)])
}

// StartTX begins a new voice transmission.
func (c *YSFClient) StartTX() {
	c.mu.Lock()
	c.txCounter = 0
	c.txFN = 0
	c.mu.Unlock()

	// Send header frame
	var emptyPayload [90]byte
	pkt := BuildYSFD(c.callsign, c.callsign, "ALL", 0, false,
		YSF_FI_HEADER, 0, YSFFramesPerSuper-1, YSF_DT_VD2, 0, emptyPayload)
	c.conn.Write(pkt)

	c.mu.Lock()
	c.txCounter = 1
	c.mu.Unlock()

	log.Println("[YSF] TX started")
}

// SendVoice sends 5 AMBE frames as a single YSFD communication packet.
func (c *YSFClient) SendVoice(ambeFrames [5][9]byte) error {
	c.mu.Lock()
	counter := c.txCounter
	c.txCounter = (c.txCounter + 1) & 0x7F
	fn := c.txFN
	c.txFN = (c.txFN + 1) % YSFFramesPerSuper
	c.mu.Unlock()

	payload := PackVD2AMBE(ambeFrames)
	pkt := BuildYSFD(c.callsign, c.callsign, "ALL", counter, false,
		YSF_FI_COMM, fn, YSFFramesPerSuper-1, YSF_DT_VD2, 0, payload)
	_, err := c.conn.Write(pkt)
	return err
}

// StopTX ends the current voice transmission.
func (c *YSFClient) StopTX() error {
	c.mu.Lock()
	counter := c.txCounter
	c.txCounter = (c.txCounter + 1) & 0x7F
	c.mu.Unlock()

	var emptyPayload [90]byte
	pkt := BuildYSFD(c.callsign, c.callsign, "ALL", counter, true,
		YSF_FI_TERM, 0, YSFFramesPerSuper-1, YSF_DT_VD2, 0, emptyPayload)
	_, err := c.conn.Write(pkt)
	if err != nil {
		return err
	}
	log.Println("[YSF] TX stopped")
	return nil
}

// RunReader reads incoming packets and dispatches voice frames.
func (c *YSFClient) RunReader() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.done)
		}
		c.mu.Unlock()
	}()

	missedPolls := 0
	buf := make([]byte, 512)
	for {
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := c.conn.Read(buf)
		if err != nil {
			if c.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				missedPolls++
				if missedPolls >= 4 { // 2 minutes with no data
					log.Println("[YSF] No response from reflector, disconnecting")
					return
				}
				continue
			}
			log.Printf("[YSF] Read error: %v", err)
			return
		}
		missedPolls = 0

		data := buf[:n]

		// Poll response
		if n >= 4 && string(data[0:4]) == "YSFP" {
			continue
		}

		// Voice/data frame
		if n >= YSFFrameSize && string(data[0:4]) == "YSFD" {
			frame := ParseYSFD(data)
			if frame == nil {
				continue
			}

			// Skip our own transmissions
			srcCS := strings.TrimSpace(frame.SrcGateway)
			if strings.EqualFold(srcCS, c.callsign) {
				continue
			}

			// Only process voice frames (VD Mode 2)
			if frame.FI == YSF_FI_COMM && frame.DT == YSF_DT_VD2 {
				if c.onVoiceFrame != nil {
					c.onVoiceFrame(frame)
				}
			}
			continue
		}
	}
}

// RunKeepalive sends periodic poll packets.
func (c *YSFClient) RunKeepalive() {
	ticker := time.NewTicker(time.Duration(YSFKeepaliveInterval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if c.isClosed() {
			return
		}
		if _, err := c.conn.Write(BuildYSFPoll(c.callsign)); err != nil {
			log.Printf("[YSF] Keepalive error: %v", err)
			return
		}
	}
}

func (c *YSFClient) readPacket(timeout time.Duration) ([]byte, error) {
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 512)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (c *YSFClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *YSFClient) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	c.mu.Unlock()

	if c.conn != nil {
		// Send unlink 3 times for reliability
		for i := 0; i < 3; i++ {
			c.conn.Write(BuildYSFUnlink(c.callsign))
			time.Sleep(50 * time.Millisecond)
		}
		c.conn.Close()
	}
}
