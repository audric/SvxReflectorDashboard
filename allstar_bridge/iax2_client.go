package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// IAX2Client manages the IAX2 connection to an AllStar node.
type IAX2Client struct {
	host     string
	port     int
	node     string // AllStar node number
	password string
	server   string // AllStar server hostname
	callsign string

	conn *net.UDPConn

	srcCallNo  uint16
	dstCallNo  uint16
	oseq       byte
	iseq       byte
	callStart  time.Time
	callToken  []byte
	registered bool

	// Callbacks
	onAudio func(pcm []int16)

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewIAX2Client(host string, port int, node, password, server, callsign string) *IAX2Client {
	return &IAX2Client{
		host:      host,
		port:      port,
		node:      node,
		password:  password,
		server:    server,
		callsign:  callsign,
		srcCallNo: 1,
	}
}

func (c *IAX2Client) SetAudioCallback(cb func(pcm []int16)) {
	c.onAudio = cb
}

func (c *IAX2Client) Done() <-chan struct{} {
	return c.done
}

func (c *IAX2Client) timestamp() uint32 {
	return uint32(time.Since(c.callStart).Milliseconds())
}

func (c *IAX2Client) sendFull(frameType, subclass byte, payload []byte) error {
	c.mu.Lock()
	pkt := BuildFullFrame(c.srcCallNo, c.dstCallNo, c.timestamp(), c.oseq, c.iseq, frameType, subclass, payload)
	c.oseq++
	c.mu.Unlock()
	_, err := c.conn.Write(pkt)
	return err
}

func (c *IAX2Client) sendACK(ts uint32) {
	c.mu.Lock()
	pkt := BuildFullFrame(c.srcCallNo, c.dstCallNo, ts, c.oseq, c.iseq, AST_FRAME_IAX, IAX_COMMAND_ACK, nil)
	// ACK doesn't increment oseq
	c.mu.Unlock()
	c.conn.Write(pkt)
}

// Connect performs the IAX2 call setup to the AllStar node.
func (c *IAX2Client) Connect() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.callStart = time.Now()
	c.oseq = 0
	c.iseq = 0
	c.dstCallNo = 0
	c.mu.Unlock()

	addr := net.JoinHostPort(c.host, fmt.Sprintf("%d", c.port))
	log.Printf("[IAX2] Connecting to %s node %s...", addr, c.node)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial: %w", err)
	}
	c.conn = conn

	// Step 1: Request call token
	tokenReqIEs := BuildIE(IAX_IE_CALLTOKEN, nil) // empty token = request
	if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_NEW, tokenReqIEs); err != nil {
		return fmt.Errorf("send token request: %w", err)
	}

	// Step 2: Receive CALLTOKEN
	frame, err := c.readFullFrame(5 * time.Second)
	if err != nil {
		return fmt.Errorf("read calltoken: %w", err)
	}
	if frame.Subclass != IAX_COMMAND_CALLTOKEN {
		return fmt.Errorf("expected CALLTOKEN, got frametype=%d subclass=%d", frame.FrameType, frame.Subclass)
	}
	c.callToken = frame.IEs[IAX_IE_CALLTOKEN]
	c.dstCallNo = frame.SrcCallNo
	c.mu.Lock()
	c.iseq = frame.OSeqno + 1
	c.mu.Unlock()
	c.sendACK(frame.Timestamp)
	log.Println("[IAX2] Got call token")

	// Step 3: Send NEW with full IEs
	var ies []byte
	ies = append(ies, BuildIEUint16(IAX_IE_VERSION, 2)...)
	ies = append(ies, BuildIEString(IAX_IE_CALLED_NUMBER, c.node)...)
	ies = append(ies, BuildIEString(IAX_IE_CALLING_NUMBER, c.node)...)
	ies = append(ies, BuildIEString(IAX_IE_CALLING_NAME, c.callsign)...)
	ies = append(ies, BuildIEString(IAX_IE_USERNAME, c.node)...)
	ies = append(ies, BuildIEUint32(IAX_IE_FORMAT, AST_FORMAT_ULAW)...)
	ies = append(ies, BuildIE(IAX_IE_CALLTOKEN, c.callToken)...)

	c.mu.Lock()
	c.dstCallNo = 0 // reset for new call
	c.mu.Unlock()
	if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_NEW, ies); err != nil {
		return fmt.Errorf("send NEW: %w", err)
	}

	// Step 4: Handle auth or accept
	for {
		frame, err = c.readFullFrame(10 * time.Second)
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		c.mu.Lock()
		if c.dstCallNo == 0 {
			c.dstCallNo = frame.SrcCallNo
		}
		c.iseq = frame.OSeqno + 1
		c.mu.Unlock()

		if frame.FrameType == AST_FRAME_IAX {
			switch frame.Subclass {
			case IAX_COMMAND_AUTHREQ:
				// MD5 challenge
				challenge := IEString(frame.IEs, IAX_IE_CHALLENGE)
				md5Result := MD5Auth(challenge, c.password)
				c.sendACK(frame.Timestamp)

				var authIEs []byte
				authIEs = append(authIEs, BuildIEString(IAX_IE_MD5_RESULT, md5Result)...)
				if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_AUTHREP, authIEs); err != nil {
					return fmt.Errorf("send AUTHREP: %w", err)
				}
				log.Println("[IAX2] Auth challenge answered")
				continue

			case IAX_COMMAND_ACCEPT:
				c.sendACK(frame.Timestamp)
				log.Println("[IAX2] Call accepted")
				continue

			case IAX_COMMAND_REJECT:
				cause := IEString(frame.IEs, IAX_IE_CAUSE)
				return fmt.Errorf("call rejected: %s", cause)

			case IAX_COMMAND_CALLTOKEN:
				// Second token (for the actual call)
				c.callToken = frame.IEs[IAX_IE_CALLTOKEN]
				c.sendACK(frame.Timestamp)
				continue
			}
		}

		if frame.FrameType == AST_FRAME_CONTROL {
			c.sendACK(frame.Timestamp)
			if frame.Subclass == AST_CONTROL_ANSWER || frame.Subclass == AST_CONTROL_RINGING {
				log.Printf("[IAX2] Connected to AllStar node %s", c.node)
				return nil
			}
		}

		// ACK anything else
		c.sendACK(frame.Timestamp)
	}
}

// SendAudio sends a ulaw audio frame as a mini frame.
func (c *IAX2Client) SendAudio(ulaw []byte) error {
	ts := uint16(c.timestamp() & 0xFFFF)
	pkt := BuildMiniFrame(c.srcCallNo, ts, ulaw)
	_, err := c.conn.Write(pkt)
	return err
}

// SendKey sends a PTT key (start TX) control frame.
func (c *IAX2Client) SendKey() error {
	return c.sendFull(AST_FRAME_CONTROL, AST_CONTROL_KEY, nil)
}

// SendUnkey sends a PTT unkey (stop TX) control frame.
func (c *IAX2Client) SendUnkey() error {
	return c.sendFull(AST_FRAME_CONTROL, AST_CONTROL_UNKEY, nil)
}

// RunReader reads incoming packets and dispatches audio/control frames.
func (c *IAX2Client) RunReader() {
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
				if missedPings >= 4 {
					log.Println("[IAX2] No response from server, disconnecting")
					return
				}
				continue
			}
			log.Printf("[IAX2] Read error: %v", err)
			return
		}
		missedPings = 0

		data := buf[:n]
		if n < 4 {
			continue
		}

		// Mini frame (F bit clear)
		if data[0]&0x80 == 0 {
			if n > 4 && c.onAudio != nil {
				pcm := UlawToPCM(data[4:])
				c.onAudio(pcm)
			}
			continue
		}

		// Full frame
		if n < 12 {
			continue
		}
		frame := ParseFullFrame(data)
		if frame == nil {
			continue
		}

		c.mu.Lock()
		c.iseq = frame.OSeqno + 1
		c.mu.Unlock()

		switch frame.FrameType {
		case AST_FRAME_IAX:
			switch frame.Subclass {
			case IAX_COMMAND_PING:
				// Respond with PONG
				var pongIEs []byte
				pongIEs = append(pongIEs, BuildIEUint32(IAX_IE_RR_JITTER, 0)...)
				pongIEs = append(pongIEs, BuildIEUint32(IAX_IE_RR_LOSS, 0)...)
				pongIEs = append(pongIEs, BuildIEUint32(IAX_IE_RR_PKTS, 0)...)
				pongIEs = append(pongIEs, BuildIEUint16(IAX_IE_RR_DELAY, 0)...)
				pongIEs = append(pongIEs, BuildIEUint32(IAX_IE_RR_DROPPED, 0)...)
				pongIEs = append(pongIEs, BuildIEUint32(IAX_IE_RR_OOO, 0)...)
				c.sendFull(AST_FRAME_IAX, IAX_COMMAND_PONG, pongIEs)
			case IAX_COMMAND_LAGRQ:
				c.sendFull(AST_FRAME_IAX, IAX_COMMAND_LAGRP, nil)
			case IAX_COMMAND_ACK:
				// nothing
			case IAX_COMMAND_HANGUP, IAX_COMMAND_REJECT, IAX_COMMAND_INVAL:
				log.Printf("[IAX2] Server disconnected: subclass=%d", frame.Subclass)
				return
			}

		case AST_FRAME_VOICE:
			if frame.Subclass == AST_FORMAT_ULAW && len(frame.RawPayload) > 0 && c.onAudio != nil {
				pcm := UlawToPCM(frame.RawPayload)
				c.onAudio(pcm)
			}
			c.sendACK(frame.Timestamp)

		case AST_FRAME_CONTROL:
			c.sendACK(frame.Timestamp)
			if frame.Subclass == AST_CONTROL_HANGUP {
				log.Println("[IAX2] Server hangup")
				return
			}
		}
	}
}

func (c *IAX2Client) readFullFrame(timeout time.Duration) (*IAX2FullFrame, error) {
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if n < 12 || buf[0]&0x80 == 0 {
			continue // skip mini frames during handshake
		}
		frame := ParseFullFrame(buf[:n])
		if frame != nil {
			return frame, nil
		}
	}
}

func (c *IAX2Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *IAX2Client) Close() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed && c.done != nil {
		close(c.done)
	}
	c.mu.Unlock()

	if c.conn != nil {
		// Send hangup
		var hangupIEs []byte
		hangupIEs = append(hangupIEs, BuildIEString(IAX_IE_CAUSE, "bridge shutdown")...)
		c.sendFull(AST_FRAME_IAX, IAX_COMMAND_HANGUP, hangupIEs)
		time.Sleep(50 * time.Millisecond)
		c.conn.Close()
	}
}
