package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("SVX Audio Bridge starting...")

	host := envRequired("REFLECTOR_HOST")
	port := envInt("REFLECTOR_PORT", 5300)
	redisURL := envDefault("REDIS_URL", "redis://redis:6379/1")

	log.Printf("Config: host=%s port=%d", host, port)

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Invalid REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opts)
	ctx := context.Background()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis connection failed: %v", err)
	}
	log.Println("Connected to Redis")

	reg := &registry{
		host:        host,
		port:        port,
		rdb:         rdb,
		ctx:         ctx,
		sessions:    make(map[string]*entry),
		tgPublisher: make(map[uint32]string),
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sub := rdb.Subscribe(ctx, "audio:commands", "audio:tx")
	defer sub.Close()

	go func() {
		ch := sub.Channel()
		for msg := range ch {
			var cmd struct {
				Action       string `json:"action"`
				TG           int    `json:"tg"`
				Callsign     string `json:"callsign"`
				AuthKey      string `json:"auth_key"`
				Audio        string `json:"audio"`
				SW           string `json:"sw"`
				SWVer        string `json:"sw_ver"`
				NodeClass    string `json:"node_class"`
				NodeLocation string `json:"node_location"`
				Sysop        string `json:"sysop"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				log.Printf("Invalid command: %v", err)
				continue
			}

			switch cmd.Action {
			case "connect":
				if cmd.Callsign != "" && cmd.TG > 0 {
					nodeInfo := map[string]string{
						"sw":           cmd.SW,
						"swVer":        cmd.SWVer,
						"nodeClass":    cmd.NodeClass,
						"nodeLocation": cmd.NodeLocation,
						"sysop":        cmd.Sysop,
					}
					reg.connect(cmd.Callsign, cmd.AuthKey, uint32(cmd.TG), nodeInfo)
				}
			case "disconnect":
				if cmd.Callsign != "" {
					reg.disconnect(cmd.Callsign)
				}
			case "select_tg":
				if cmd.TG > 0 && cmd.Callsign != "" {
					reg.selectTG(cmd.Callsign, uint32(cmd.TG))
				}
			case "ptt_start":
				if cmd.Callsign != "" {
					tg := uint32(cmd.TG)
					if tg == 0 {
						tg = reg.currentTG(cmd.Callsign)
					}
					reg.sendTalkerStart(cmd.Callsign, tg)
				}
			case "ptt_stop":
				if cmd.Callsign != "" {
					tg := uint32(cmd.TG)
					if tg == 0 {
						tg = reg.currentTG(cmd.Callsign)
					}
					reg.sendTalkerStop(cmd.Callsign, tg)
				}
			case "audio":
				if cmd.Audio != "" && cmd.Callsign != "" {
					reg.sendAudio(cmd.Callsign, cmd.Audio)
				}
			}
		}
	}()

	log.Println("Waiting for tune-in commands...")
	<-sigCh
	log.Println("Shutting down...")
	reg.stopAll()
	rdb.Close()
}

// entry represents a single web listener's reflector connection.
type entry struct {
	client   *Client
	callsign string
	authKey  string
	tg       uint32
	nodeInfo map[string]string
}

// registry manages multiple concurrent reflector sessions keyed by callsign.
type registry struct {
	host string
	port int
	rdb  *redis.Client
	ctx  context.Context

	mu          sync.Mutex
	sessions    map[string]*entry  // keyed by callsign
	tgPublisher map[uint32]string  // callsign publishing audio for each TG
}

func (r *registry) connect(callsign string, authKey string, tg uint32, nodeInfo map[string]string) {
	r.mu.Lock()
	// If this callsign already has a session, close the old one (reconnect)
	if old, ok := r.sessions[callsign]; ok {
		log.Printf("Replacing existing session for %s", callsign)
		old.client.Close()
		r.removePublisher(callsign, old.tg)
		delete(r.sessions, callsign)
	}
	r.mu.Unlock()

	c := NewClient(r.host, r.port, authKey, callsign)
	c.SetNodeInfo(nodeInfo)
	if err := c.Connect(); err != nil {
		log.Printf("Connect failed for %s: %v", callsign, err)
		return
	}

	if err := c.SelectTG(tg); err != nil {
		log.Printf("SelectTG error for %s: %v", callsign, err)
		c.Close()
		return
	}

	e := &entry{
		client:   c,
		callsign: callsign,
		authKey:  authKey,
		tg:       tg,
		nodeInfo: nodeInfo,
	}

	c.SetAudioCallback(func(opusFrame []byte) {
		r.mu.Lock()
		// Only publish if this session is the designated publisher for its TG
		currentTG := e.tg
		publisher := r.tgPublisher[currentTG]
		r.mu.Unlock()

		if publisher != callsign {
			return
		}

		channel := fmt.Sprintf("audio:tg:%d", currentTG)
		encoded := base64.StdEncoding.EncodeToString(opusFrame)
		msg := map[string]interface{}{
			"audio": encoded,
			"seq":   time.Now().UnixMilli(),
		}
		payload, _ := json.Marshal(msg)
		r.rdb.Publish(r.ctx, channel, payload)
	})

	r.mu.Lock()
	r.sessions[callsign] = e
	// Become publisher for this TG if there isn't one already
	if _, ok := r.tgPublisher[tg]; !ok {
		r.tgPublisher[tg] = callsign
	}
	r.mu.Unlock()

	// Start protocol goroutines; if TCPReader dies the session is dead
	go func() {
		c.RunTCPReader()
		r.cleanup(callsign, c)
	}()
	go c.RunTCPHeartbeat()
	go c.RunUDPReader()
	go c.RunUDPHeartbeat()

	log.Printf("Session started: callsign=%s tg=%d", callsign, tg)
}

func (r *registry) disconnect(callsign string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.sessions[callsign]
	if !ok {
		return
	}

	log.Printf("Session stopped: callsign=%s", callsign)
	e.client.Close()
	r.removePublisher(callsign, e.tg)
	delete(r.sessions, callsign)
}

// cleanup is called when the reflector connection drops unexpectedly.
// Only cleans up if the stored client matches (guards against reconnect races).
func (r *registry) cleanup(callsign string, c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.sessions[callsign]
	if !ok || e.client != c {
		return
	}

	log.Printf("Session lost (reflector disconnected): callsign=%s", callsign)
	r.removePublisher(callsign, e.tg)
	delete(r.sessions, callsign)
}

func (r *registry) selectTG(callsign string, tg uint32) {
	r.mu.Lock()
	e, ok := r.sessions[callsign]
	if !ok {
		r.mu.Unlock()
		return
	}

	oldTG := e.tg
	e.tg = tg
	c := e.client

	// Update publisher tracking
	r.removePublisher(callsign, oldTG)
	if _, ok := r.tgPublisher[tg]; !ok {
		r.tgPublisher[tg] = callsign
	}
	r.mu.Unlock()

	if err := c.SelectTG(tg); err != nil {
		log.Printf("SelectTG error for %s: %v", callsign, err)
	}
}

func (r *registry) currentTG(callsign string) uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[callsign]; ok {
		return e.tg
	}
	return 0
}

func (r *registry) sendTalkerStart(callsign string, tg uint32) {
	r.mu.Lock()
	e, ok := r.sessions[callsign]
	r.mu.Unlock()
	if ok {
		if err := e.client.SendTalkerStart(tg, callsign); err != nil {
			log.Printf("SendTalkerStart error for %s: %v", callsign, err)
		}
	}
}

func (r *registry) sendTalkerStop(callsign string, tg uint32) {
	r.mu.Lock()
	e, ok := r.sessions[callsign]
	r.mu.Unlock()
	if ok {
		if err := e.client.SendTalkerStop(tg, callsign); err != nil {
			log.Printf("SendTalkerStop error for %s: %v", callsign, err)
		}
	}
}

func (r *registry) sendAudio(callsign string, b64 string) {
	opusData, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		log.Printf("TX audio base64 decode error: %v", err)
		return
	}
	r.mu.Lock()
	e, ok := r.sessions[callsign]
	r.mu.Unlock()
	if ok {
		if err := e.client.SendAudio(opusData); err != nil {
			log.Printf("SendAudio error for %s: %v", callsign, err)
		}
	}
}

// removePublisher removes callsign as the TG publisher and promotes another session if available.
// Must be called with r.mu held.
func (r *registry) removePublisher(callsign string, tg uint32) {
	if r.tgPublisher[tg] != callsign {
		return
	}
	delete(r.tgPublisher, tg)
	// Promote another session on the same TG
	for cs, e := range r.sessions {
		if e.tg == tg && cs != callsign {
			r.tgPublisher[tg] = cs
			log.Printf("Promoted %s as publisher for TG %d", cs, tg)
			break
		}
	}
}

func (r *registry) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for cs, e := range r.sessions {
		log.Printf("Stopping session: callsign=%s", cs)
		e.client.Close()
	}
	r.sessions = make(map[string]*entry)
	r.tgPublisher = make(map[uint32]string)
}

func envRequired(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("Required env var %s is not set", key)
	}
	return v
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("Invalid integer for %s: %v", key, err)
		}
		return n
	}
	return fallback
}
