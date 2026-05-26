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

	// out is the current outgoing audio stream. It is opened per over
	// (StartTransmit) and closed at end-of-over (StopTransmit) so gumble emits
	// the stream terminator and resets the Opus encoder between overs.
	out   chan<- gumble.AudioBuffer
	outMu sync.Mutex

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
			} else if root := e.Client.Channels[0]; root != nil {
				// Channel missing — create it. The bot is a registered (@auth)
				// user, granted MakeTempChannel, so create a temporary channel
				// under Root; we move in when the ChannelChange event arrives.
				log.Printf("[Mumble] Channel %q not found; creating it", m.channel)
				root.Add(m.channel, true)
			}
		},
		ChannelChange: func(e *gumble.ChannelChangeEvent) {
			if e.Channel == nil || e.Channel.Name != m.channel {
				return
			}
			if e.Client.Self != nil && e.Client.Self.Channel != e.Channel {
				e.Client.Self.Move(e.Channel)
				log.Printf("[Mumble] Moved into channel %q (created)", m.channel)
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

// StartTransmit opens a fresh outgoing audio stream. Call at the start of an over.
func (m *MumbleClient) StartTransmit() {
	m.outMu.Lock()
	defer m.outMu.Unlock()
	if m.out != nil || m.client == nil {
		return
	}
	m.out = m.client.AudioOutgoing()
}

// StopTransmit closes the outgoing audio stream so gumble flushes the stream
// terminator and resets the Opus encoder. Call at end-of-over.
func (m *MumbleClient) StopTransmit() {
	m.outMu.Lock()
	out := m.out
	m.out = nil
	m.outMu.Unlock()
	if out == nil {
		return
	}
	defer func() { recover() }() // tolerate a close racing with shutdown
	close(out)
}

// SendPCM transmits one frame of 48kHz mono int16 PCM, if a transmission is open.
func (m *MumbleClient) SendPCM(pcm []int16) {
	m.outMu.Lock()
	out := m.out
	m.outMu.Unlock()
	if out == nil {
		return
	}
	defer func() { recover() }() // guard against send on a closed channel during shutdown
	out <- gumble.AudioBuffer(pcm)
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
	m.StopTransmit()
	m.signalDone()
	if m.client != nil {
		m.client.Disconnect()
	}
}
