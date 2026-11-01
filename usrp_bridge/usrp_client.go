package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// USRPClient speaks the USRP UDP protocol to a DVSwitch peer (Analog_Bridge,
// MMDVM_Bridge, or another svxlink UsrpLogic). It binds a local socket on
// rxPort to receive and sends to host:txPort. It is a low-level transport:
// talk-state arbitration and the audio pipeline live in main.go, mirroring the
// structure of the other bridges.
type USRPClient struct {
	host    string
	txPort  int
	rxPort  int
	tg      uint32
	remote  *net.UDPAddr
	conn    *net.UDPConn
	seq     uint32
	sendMu  sync.Mutex
	stateMu sync.Mutex
	closed  bool
	done    chan struct{}

	// Callbacks
	onVoice    func(pcm []int16, keyup bool)
	onMetadata func(callsign string)
}

func NewUSRPClient(host string, txPort, rxPort int, tg uint32) *USRPClient {
	return &USRPClient{host: host, txPort: txPort, rxPort: rxPort, tg: tg}
}

// SetVoiceCallback registers a handler for inbound voice frames (8 kHz PCM).
// keyup reflects the packet's PTT flag; a keyup=false frame marks PTT release.
func (u *USRPClient) SetVoiceCallback(cb func(pcm []int16, keyup bool)) { u.onVoice = cb }

// SetMetadataCallback registers a handler for inbound talker metadata.
func (u *USRPClient) SetMetadataCallback(cb func(callsign string)) { u.onMetadata = cb }

func (u *USRPClient) Done() <-chan struct{} { return u.done }

func (u *USRPClient) Connect() error {
	u.stateMu.Lock()
	u.closed = false
	u.done = make(chan struct{})
	u.stateMu.Unlock()

	remote, err := net.ResolveUDPAddr("udp", net.JoinHostPort(u.host, fmt.Sprintf("%d", u.txPort)))
	if err != nil {
		return fmt.Errorf("resolve USRP peer: %w", err)
	}
	u.remote = remote

	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: u.rxPort})
	if err != nil {
		return fmt.Errorf("bind USRP rx port %d: %w", u.rxPort, err)
	}
	u.conn = conn
	log.Printf("[USRP] Listening on :%d, sending to %s", u.rxPort, remote)
	return nil
}

func (u *USRPClient) nextSeq() uint32 {
	u.sendMu.Lock()
	seq := u.seq
	u.seq++
	u.sendMu.Unlock()
	return seq
}

// SendVoice transmits an 8 kHz PCM frame with PTT active.
func (u *USRPClient) SendVoice(pcm []int16) error {
	_, err := u.conn.WriteToUDP(buildVoice(u.nextSeq(), true, u.tg, pcm), u.remote)
	return err
}

// SendStop transmits the end-of-transmission (keyup=0) packet.
func (u *USRPClient) SendStop() error {
	_, err := u.conn.WriteToUDP(buildStop(u.nextSeq(), u.tg), u.remote)
	return err
}

// SendMetadata transmits a talker-info TLV so the peer can show the callsign.
func (u *USRPClient) SendMetadata(callsign string) error {
	_, err := u.conn.WriteToUDP(buildMetadata(u.nextSeq(), u.tg, callsign), u.remote)
	return err
}

func (u *USRPClient) RunReader() {
	defer func() {
		u.stateMu.Lock()
		if !u.closed {
			u.closed = true
			close(u.done)
		}
		u.stateMu.Unlock()
	}()

	buf := make([]byte, 4096)
	for {
		u.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, _, err := u.conn.ReadFromUDP(buf)
		if err != nil {
			if u.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("[USRP] read error: %v", err)
			return
		}

		h, ok := parseHeader(buf[:n])
		if !ok {
			continue
		}
		switch h.Type {
		case USRPTypeVoice:
			if u.onVoice != nil {
				u.onVoice(parseVoice(buf[:n]), h.Keyup)
			}
		case USRPTypeText:
			if cs := parseMetadataCallsign(buf[:n]); cs != "" && u.onMetadata != nil {
				u.onMetadata(cs)
			}
		}
	}
}

func (u *USRPClient) isClosed() bool {
	u.stateMu.Lock()
	defer u.stateMu.Unlock()
	return u.closed
}

func (u *USRPClient) Close() {
	u.stateMu.Lock()
	wasClosed := u.closed
	u.closed = true
	if !wasClosed && u.done != nil {
		close(u.done)
	}
	u.stateMu.Unlock()
	if u.conn != nil {
		u.conn.Close()
	}
}
