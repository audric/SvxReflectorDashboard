package main

import (
	"crypto/tls"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumbleutil"
	_ "layeh.com/gumble/opus" // registers the Opus codec (CGo/libopus)
)

// MumbleClient is a thin wrapper around a gumble bot client.
type MumbleClient struct {
	host    string
	port    int
	user    string
	pass    string
	channel string

	client *gumble.Client
	out    chan<- gumble.AudioBuffer

	// Callbacks
	onStreamStart func(sender string)
	onAudio       func(sender string, pcm []int16)
	onStreamStop  func(sender string)

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewMumbleClient(host string, port int, user, pass, channel string) *MumbleClient {
	return &MumbleClient{host: host, port: port, user: user, pass: pass, channel: channel}
}

func (m *MumbleClient) Done() <-chan struct{}                     { return m.done }
func (m *MumbleClient) SetStreamStartCallback(cb func(string))    { m.onStreamStart = cb }
func (m *MumbleClient) SetAudioCallback(cb func(string, []int16)) { m.onAudio = cb }
func (m *MumbleClient) SetStreamStopCallback(cb func(string))     { m.onStreamStop = cb }

func (m *MumbleClient) Connect() error {
	m.mu.Lock()
	m.closed = false
	m.done = make(chan struct{})
	m.mu.Unlock()

	config := gumble.NewConfig()
	config.Username = m.user
	config.Password = m.pass

	config.Attach(gumbleutil.Listener{
		Connect: func(e *gumble.ConnectEvent) {
			log.Printf("[Mumble] Connected as %s", m.user)
			if ch := e.Client.Channels.Find(m.channel); ch != nil {
				e.Client.Self.Move(ch)
				log.Printf("[Mumble] Moved into channel %q", m.channel)
			} else {
				log.Printf("[Mumble] WARNING: channel %q not found; staying in root", m.channel)
			}
		},
		Disconnect: func(e *gumble.DisconnectEvent) {
			log.Printf("[Mumble] Disconnected: %s", e.String)
			m.signalDone()
		},
	})
	config.AttachAudio(m)

	addr := net.JoinHostPort(m.host, strconv.Itoa(m.port))
	tlsCfg := &tls.Config{InsecureSkipVerify: true}
	client, err := gumble.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, addr, config, tlsCfg)
	if err != nil {
		return err
	}
	m.client = client
	m.out = client.AudioOutgoing()
	return nil
}

// OnAudioStream implements gumble.AudioListener. One goroutine per talker.
func (m *MumbleClient) OnAudioStream(e *gumble.AudioStreamEvent) {
	sender := ""
	if e.User != nil {
		sender = strings.TrimSpace(e.User.Name)
	}
	if m.onStreamStart != nil {
		m.onStreamStart(sender)
	}
	for pkt := range e.C { // ranges until the talker stops (channel closed)
		if m.onAudio != nil && len(pkt.AudioBuffer) > 0 {
			m.onAudio(sender, []int16(pkt.AudioBuffer))
		}
	}
	if m.onStreamStop != nil {
		m.onStreamStop(sender)
	}
}

// SendPCM transmits one frame of 48kHz mono int16 PCM to the channel.
func (m *MumbleClient) SendPCM(pcm []int16) {
	if m.out == nil {
		return
	}
	defer func() { recover() }() // guard against send on closed channel during shutdown
	m.out <- gumble.AudioBuffer(pcm)
}

func (m *MumbleClient) signalDone() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		close(m.done)
	}
	m.mu.Unlock()
}

func (m *MumbleClient) Close() {
	m.signalDone()
	if m.client != nil {
		m.client.Disconnect()
	}
}
