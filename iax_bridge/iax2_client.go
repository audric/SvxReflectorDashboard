package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// IAX2Client manages an IAX2 connection to a generic Asterisk PBX.
type IAX2Client struct {
	host      string
	port      int
	username  string
	password  string
	extension string
	context   string
	callsign  string
	codecs    uint32 // bitmask of supported codecs

	conn *net.UDPConn

	srcCallNo  uint16
	dstCallNo  uint16
	oseq       byte
	iseq       byte
	callStart  time.Time
	callToken  []byte
	registered bool
	activeCodec Codec // codec negotiated for the active call

	// Callbacks
	onAudio func(pcm []int16)

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewIAX2Client(host string, port int, username, password, extension, context, callsign string, codecs uint32) *IAX2Client {
	return &IAX2Client{
		host:      host,
		port:      port,
		username:  username,
		password:  password,
		extension: extension,
		context:   context,
		callsign:  callsign,
		codecs:    codecs,
		srcCallNo: 1,
	}
}

func (c *IAX2Client) SetAudioCallback(cb func(pcm []int16)) {
	c.onAudio = cb
}

func (c *IAX2Client) Done() <-chan struct{} {
	return c.done
}

func (c *IAX2Client) ActiveCodec() Codec {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeCodec
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
	c.mu.Unlock()
	c.conn.Write(pkt)
}

// Register performs IAX2 registration with the server.
func (c *IAX2Client) Register() error {
	c.mu.Lock()
	c.closed = false
	c.done = make(chan struct{})
	c.callStart = time.Now()
	c.oseq = 0
	c.iseq = 0
	c.dstCallNo = 0
	c.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	log.Printf("[IAX2] Registering %s@%s...", c.username, addr)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve UDP: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial: %w", err)
	}
	c.conn = conn

	// REGREQ with username
	var ies []byte
	ies = append(ies, BuildIEString(IAX_IE_USERNAME, c.username)...)
	ies = append(ies, BuildIEUint16(IAX_IE_REFRESH, 60)...)
	if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_REGREQ, ies); err != nil {
		return fmt.Errorf("send REGREQ: %w", err)
	}

	// Wait for REGAUTH or REGACK
	for {
		frame, err := c.readFullFrame(10 * time.Second)
		if err != nil {
			return fmt.Errorf("read reg response: %w", err)
		}

		c.mu.Lock()
		if c.dstCallNo == 0 {
			c.dstCallNo = frame.SrcCallNo
		}
		c.iseq = frame.OSeqno + 1
		c.mu.Unlock()

		if frame.FrameType == AST_FRAME_IAX {
			switch frame.Subclass {
			case IAX_COMMAND_REGAUTH:
				challenge := IEString(frame.IEs, IAX_IE_CHALLENGE)
				c.sendACK(frame.Timestamp)

				var authIEs []byte
				authIEs = append(authIEs, BuildIEString(IAX_IE_USERNAME, c.username)...)
				authIEs = append(authIEs, BuildIEString(IAX_IE_MD5_RESULT, MD5Auth(challenge, c.password))...)
				authIEs = append(authIEs, BuildIEUint16(IAX_IE_REFRESH, 60)...)
				if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_REGREQ, authIEs); err != nil {
					return fmt.Errorf("send REGREQ auth: %w", err)
				}
				continue

			case IAX_COMMAND_REGACK:
				c.sendACK(frame.Timestamp)
				c.registered = true
				log.Println("[IAX2] Registration successful")
				return nil

			case IAX_COMMAND_REGREJ:
				cause := IEString(frame.IEs, IAX_IE_CAUSE)
				return fmt.Errorf("registration rejected: %s", cause)

			case IAX_COMMAND_CALLTOKEN:
				c.callToken = frame.IEs[IAX_IE_CALLTOKEN]
				c.sendACK(frame.Timestamp)
				var retryIEs []byte
				retryIEs = append(retryIEs, BuildIEString(IAX_IE_USERNAME, c.username)...)
				retryIEs = append(retryIEs, BuildIEUint16(IAX_IE_REFRESH, 60)...)
				retryIEs = append(retryIEs, BuildIE(IAX_IE_CALLTOKEN, c.callToken)...)
				if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_REGREQ, retryIEs); err != nil {
					return fmt.Errorf("send REGREQ with token: %w", err)
				}
				continue
			}
		}
		c.sendACK(frame.Timestamp)
	}
}

// PlaceCall initiates a call to the configured extension@context.
func (c *IAX2Client) PlaceCall() error {
	c.mu.Lock()
	c.srcCallNo++
	if c.srcCallNo > 0x7FFE {
		c.srcCallNo = 1
	}
	c.oseq = 0
	c.iseq = 0
	c.dstCallNo = 0
	c.mu.Unlock()

	c.callStart = time.Now()

	// Step 1: Request call token
	tokenReqIEs := BuildIE(IAX_IE_CALLTOKEN, nil)
	if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_NEW, tokenReqIEs); err != nil {
		return fmt.Errorf("send token request: %w", err)
	}

	// Step 2: Receive CALLTOKEN
	frame, err := c.readFullFrame(5 * time.Second)
	if err != nil {
		return fmt.Errorf("read calltoken: %w", err)
	}
	if frame.FrameType == AST_FRAME_IAX && frame.Subclass == IAX_COMMAND_CALLTOKEN {
		c.callToken = frame.IEs[IAX_IE_CALLTOKEN]
		c.mu.Lock()
		c.dstCallNo = frame.SrcCallNo
		c.iseq = frame.OSeqno + 1
		c.mu.Unlock()
		c.sendACK(frame.Timestamp)
	}

	// Step 3: Send NEW with full IEs
	var ies []byte
	ies = append(ies, BuildIEUint16(IAX_IE_VERSION, 2)...)
	ies = append(ies, BuildIEString(IAX_IE_CALLED_NUMBER, c.extension)...)
	ies = append(ies, BuildIEString(IAX_IE_CALLED_CONTEXT, c.context)...)
	ies = append(ies, BuildIEString(IAX_IE_CALLING_NUMBER, c.username)...)
	ies = append(ies, BuildIEString(IAX_IE_CALLING_NAME, c.callsign)...)
	ies = append(ies, BuildIEString(IAX_IE_USERNAME, c.username)...)
	ies = append(ies, BuildIEUint32(IAX_IE_FORMAT, firstCodec(c.codecs))...)
	ies = append(ies, BuildIEUint32(IAX_IE_CAPABILITY, c.codecs)...)
	if c.callToken != nil {
		ies = append(ies, BuildIE(IAX_IE_CALLTOKEN, c.callToken)...)
	}

	c.mu.Lock()
	c.dstCallNo = 0
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
				challenge := IEString(frame.IEs, IAX_IE_CHALLENGE)
				c.sendACK(frame.Timestamp)
				var authIEs []byte
				authIEs = append(authIEs, BuildIEString(IAX_IE_MD5_RESULT, MD5Auth(challenge, c.password))...)
				if err := c.sendFull(AST_FRAME_IAX, IAX_COMMAND_AUTHREP, authIEs); err != nil {
					return fmt.Errorf("send AUTHREP: %w", err)
				}
				continue

			case IAX_COMMAND_ACCEPT:
				if formatData, ok := frame.IEs[IAX_IE_FORMAT]; ok && len(formatData) >= 4 {
					format := uint32(formatData[0])<<24 | uint32(formatData[1])<<16 | uint32(formatData[2])<<8 | uint32(formatData[3])
					codec, cerr := CodecForFormat(format)
					if cerr != nil {
						return fmt.Errorf("server accepted unsupported codec %d: %w", format, cerr)
					}
					c.mu.Lock()
					c.activeCodec = codec
					c.mu.Unlock()
					log.Printf("[IAX2] Call accepted, codec: %s", codec.Name())
				} else {
					codec, _ := CodecForFormat(firstCodec(c.codecs))
					c.mu.Lock()
					c.activeCodec = codec
					c.mu.Unlock()
					log.Printf("[IAX2] Call accepted, using default codec: %s", codec.Name())
				}
				c.sendACK(frame.Timestamp)
				continue

			case IAX_COMMAND_REJECT:
				cause := IEString(frame.IEs, IAX_IE_CAUSE)
				return fmt.Errorf("call rejected: %s", cause)

			case IAX_COMMAND_CALLTOKEN:
				c.callToken = frame.IEs[IAX_IE_CALLTOKEN]
				c.sendACK(frame.Timestamp)
				continue
			}
		}

		if frame.FrameType == AST_FRAME_CONTROL {
			c.sendACK(frame.Timestamp)
			if frame.Subclass == AST_CONTROL_ANSWER || frame.Subclass == AST_CONTROL_RINGING {
				if c.ActiveCodec() == nil {
					codec, _ := CodecForFormat(firstCodec(c.codecs))
					c.mu.Lock()
					c.activeCodec = codec
					c.mu.Unlock()
				}
				log.Printf("[IAX2] Call connected to %s@%s", c.extension, c.context)
				return nil
			}
		}

		c.sendACK(frame.Timestamp)
	}
}

// Hangup sends a HANGUP and resets call state.
func (c *IAX2Client) Hangup() {
	var hangupIEs []byte
	hangupIEs = append(hangupIEs, BuildIEString(IAX_IE_CAUSE, "call ended")...)
	c.sendFull(AST_FRAME_IAX, IAX_COMMAND_HANGUP, hangupIEs)
	c.mu.Lock()
	c.activeCodec = nil
	c.mu.Unlock()
}

// SendAudio sends an encoded audio frame as a mini frame.
func (c *IAX2Client) SendAudio(encoded []byte) error {
	ts := uint16(c.timestamp() & 0xFFFF)
	pkt := BuildMiniFrame(c.srcCallNo, ts, encoded)
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
				codec := c.ActiveCodec()
				if codec != nil {
					pcm, err := codec.Decode(data[4:])
					if err == nil {
						c.onAudio(pcm)
					}
				}
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
			case IAX_COMMAND_NEW:
				// Incoming call — accept it
				c.mu.Lock()
				c.dstCallNo = frame.SrcCallNo
				c.mu.Unlock()
				c.sendACK(frame.Timestamp)

				var acceptIEs []byte
				acceptIEs = append(acceptIEs, BuildIEUint32(IAX_IE_FORMAT, firstCodec(c.codecs))...)
				c.sendFull(AST_FRAME_IAX, IAX_COMMAND_ACCEPT, acceptIEs)

				c.sendFull(AST_FRAME_CONTROL, AST_CONTROL_ANSWER, nil)

				codec, _ := CodecForFormat(firstCodec(c.codecs))
				c.mu.Lock()
				c.activeCodec = codec
				c.mu.Unlock()
				log.Printf("[IAX2] Accepted incoming call, codec: %s", codec.Name())
			}

		case AST_FRAME_VOICE:
			if len(frame.RawPayload) > 0 && c.onAudio != nil {
				codec := c.ActiveCodec()
				if codec != nil && frame.Subclass == byte(codec.FormatBit()) {
					pcm, err := codec.Decode(frame.RawPayload)
					if err == nil {
						c.onAudio(pcm)
					}
				}
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

// RunRegRefresh sends periodic REGREQ to keep registration alive.
func (c *IAX2Client) RunRegRefresh() {
	ticker := time.NewTicker(50 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var ies []byte
			ies = append(ies, BuildIEString(IAX_IE_USERNAME, c.username)...)
			ies = append(ies, BuildIEUint16(IAX_IE_REFRESH, 60)...)
			c.sendFull(AST_FRAME_IAX, IAX_COMMAND_REGREQ, ies)
		case <-c.done:
			return
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
			continue
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
		var hangupIEs []byte
		hangupIEs = append(hangupIEs, BuildIEString(IAX_IE_CAUSE, "bridge shutdown")...)
		c.sendFull(AST_FRAME_IAX, IAX_COMMAND_HANGUP, hangupIEs)
		time.Sleep(50 * time.Millisecond)
		c.conn.Close()
	}
}

// firstCodec returns the preferred codec from a capability bitmask.
func firstCodec(cap uint32) uint32 {
	for _, fmt := range []uint32{AST_FORMAT_GSM, AST_FORMAT_ULAW, AST_FORMAT_ALAW, AST_FORMAT_G726} {
		if cap&fmt != 0 {
			return fmt
		}
	}
	return AST_FORMAT_ULAW
}
