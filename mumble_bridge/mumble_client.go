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

// voxHangTime is how long incoming Mumble audio must be silent before we treat
// the current over as finished. gumble (this version) never closes a user's
// audio stream channel and fires OnAudioStream only once per user, so talk-spurt
// boundaries are derived from this silence gap rather than from the channel.
const voxHangTime = 300 * time.Millisecond

// MumbleClient is a thin wrapper around a gumble bot client.
type MumbleClient struct {
	host    string
	port    int
	user    string
	pass    string
	channel string
	welcome string // optional message sent to users as they join the channel

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

	// VOX state for incoming audio (Mumble -> bridge). activeTalker is the
	// callsign whose over is currently open; lastAudio is when we last saw a
	// frame from them. Guarded by voxMu.
	voxMu        sync.Mutex
	activeTalker string
	lastAudio    time.Time

	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewMumbleClient(host string, port int, user, pass, channel, welcome string) *MumbleClient {
	return &MumbleClient{host: host, port: port, user: user, pass: pass, channel: channel, welcome: welcome}
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
		UserChange: func(e *gumble.UserChangeEvent) {
			// Greet a user as they connect/move into our channel (not on mute,
			// name changes, etc.), skipping the bot itself.
			if m.welcome == "" || e.User == nil || e.Client.Self == nil || e.User == e.Client.Self {
				return
			}
			if e.Type&(gumble.UserChangeConnected|gumble.UserChangeChannel) == 0 {
				return
			}
			if e.User.Channel == nil || e.User.Channel.Name != m.channel {
				return
			}
			defer func() { recover() }() // tolerate races during shutdown
			e.Client.Send(&gumble.TextMessage{Users: []*gumble.User{e.User}, Message: m.welcome})
			log.Printf("[Mumble] Welcomed %s", e.User.Name)
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

	m.voxMu.Lock()
	m.activeTalker = ""
	m.voxMu.Unlock()
	go m.runVoxWatchdog(m.done)

	return nil
}

// OnAudioStream implements gumble.AudioListener. gumble calls this synchronously
// from its network-read goroutine and delivers the first packet only AFTER this
// returns, so draining e.C here directly would deadlock the whole client. We
// hand the stream off to a goroutine and route every frame through the VOX state
// machine. The goroutine exits when the connection (done) drops, since gumble
// never closes the stream channel itself.
func (m *MumbleClient) OnAudioStream(e *gumble.AudioStreamEvent) {
	sender := ""
	if e.User != nil {
		sender = strings.TrimSpace(e.User.Name)
	}
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	go func() {
		for {
			select {
			case <-done:
				return
			case pkt, ok := <-e.C:
				if !ok {
					return
				}
				if pkt != nil && len(pkt.AudioBuffer) > 0 {
					m.feedAudio(sender, []int16(pkt.AudioBuffer))
				}
			}
		}
	}()
}

// feedAudio routes one incoming PCM frame through VOX: the first frame after
// silence opens an over (onStreamStart), later frames extend it (onAudio), and
// runVoxWatchdog closes it after voxHangTime of no audio. First-talker-wins — a
// second concurrent talker is ignored until the active one stops.
func (m *MumbleClient) feedAudio(sender string, pcm []int16) {
	m.voxMu.Lock()
	start := false
	if m.activeTalker == "" {
		m.activeTalker = sender
		start = true
	} else if sender != m.activeTalker {
		m.voxMu.Unlock()
		return
	}
	m.lastAudio = time.Now()
	m.voxMu.Unlock()

	if start && m.onStreamStart != nil {
		m.onStreamStart(sender)
	}
	if m.onAudio != nil {
		m.onAudio(sender, pcm)
	}
}

// runVoxWatchdog closes the current over once audio stops flowing for
// voxHangTime. It exits when the connection (done) drops.
func (m *MumbleClient) runVoxWatchdog(done <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			m.voxMu.Lock()
			if m.activeTalker != "" && time.Since(m.lastAudio) > voxHangTime {
				stopped := m.activeTalker
				m.activeTalker = ""
				m.voxMu.Unlock()
				if m.onStreamStop != nil {
					m.onStreamStop(stopped)
				}
			} else {
				m.voxMu.Unlock()
			}
		}
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
