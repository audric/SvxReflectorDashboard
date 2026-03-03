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

	sess := &session{
		host: host,
		port: port,
		rdb:  rdb,
		ctx:  ctx,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sub := rdb.Subscribe(ctx, "audio:commands", "audio:tx")
	defer sub.Close()

	go func() {
		ch := sub.Channel()
		for msg := range ch {
			var cmd struct {
				Action   string `json:"action"`
				TG       int    `json:"tg"`
				Callsign string `json:"callsign"`
				AuthKey  string `json:"auth_key"`
				Audio    string `json:"audio"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				log.Printf("Invalid command: %v", err)
				continue
			}

			switch cmd.Action {
			case "connect":
				if cmd.Callsign != "" && cmd.TG > 0 {
					sess.start(cmd.Callsign, cmd.AuthKey, uint32(cmd.TG))
				}
			case "disconnect":
				sess.stop()
			case "select_tg":
				if cmd.TG > 0 {
					sess.selectTG(cmd.Callsign, uint32(cmd.TG))
				}
			case "ptt_start":
				tg := uint32(cmd.TG)
				if tg == 0 {
					tg = sess.currentTG()
				}
				sess.sendTalkerStart(tg, cmd.Callsign)
			case "ptt_stop":
				tg := uint32(cmd.TG)
				if tg == 0 {
					tg = sess.currentTG()
				}
				sess.sendTalkerStop(tg, cmd.Callsign)
			case "audio":
				if cmd.Audio != "" {
					sess.sendAudio(cmd.Audio)
				}
			}
		}
	}()

	log.Println("Waiting for tune-in commands...")
	<-sigCh
	log.Println("Shutting down...")
	sess.stop()
	rdb.Close()
}

// session manages a single on-demand reflector connection.
type session struct {
	host string
	port int
	rdb  *redis.Client
	ctx  context.Context

	mu       sync.Mutex
	client   *Client
	callsign string
	authKey  string
	tg       uint32
}

func (s *session) start(callsign string, authKey string, tg uint32) {
	s.mu.Lock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	s.mu.Unlock()

	c := NewClient(s.host, s.port, authKey, callsign)
	if err := c.Connect(); err != nil {
		log.Printf("Connect failed: %v", err)
		return
	}

	if err := c.SelectTG(tg); err != nil {
		log.Printf("SelectTG error: %v", err)
		c.Close()
		return
	}

	c.SetAudioCallback(func(opusFrame []byte) {
		s.mu.Lock()
		currentTG := s.tg
		s.mu.Unlock()

		channel := fmt.Sprintf("audio:tg:%d", currentTG)
		encoded := base64.StdEncoding.EncodeToString(opusFrame)
		msg := map[string]interface{}{
			"audio": encoded,
			"seq":   time.Now().UnixMilli(),
		}
		payload, _ := json.Marshal(msg)
		s.rdb.Publish(s.ctx, channel, payload)
	})

	s.mu.Lock()
	s.client = c
	s.callsign = callsign
	s.authKey = authKey
	s.tg = tg
	s.mu.Unlock()

	// Start protocol goroutines; if TCPReader dies the session is dead
	go func() {
		c.RunTCPReader()
		s.cleanup(c)
	}()
	go c.RunTCPHeartbeat()
	go c.RunUDPReader()
	go c.RunUDPHeartbeat()

	log.Printf("Session started: callsign=%s tg=%d", callsign, tg)
}

func (s *session) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		log.Printf("Session stopped: callsign=%s", s.callsign)
		s.client.Close()
		s.client = nil
		s.callsign = ""
	}
}

// cleanup is called when the reflector connection drops unexpectedly.
func (s *session) cleanup(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == c {
		log.Printf("Session lost (reflector disconnected)")
		s.client = nil
		s.callsign = ""
	}
}

func (s *session) selectTG(callsign string, tg uint32) {
	s.mu.Lock()

	// If no active session, start one
	if s.client == nil {
		authKey := s.authKey
		s.mu.Unlock()
		if callsign != "" {
			s.start(callsign, authKey, tg)
		}
		return
	}

	// If callsign changed, reconnect with new identity
	if callsign != "" && callsign != s.callsign {
		authKey := s.authKey
		s.mu.Unlock()
		s.start(callsign, authKey, tg)
		return
	}

	s.tg = tg
	c := s.client
	s.mu.Unlock()

	if err := c.SelectTG(tg); err != nil {
		log.Printf("SelectTG error: %v", err)
	}
}

func (s *session) currentTG() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tg
}

func (s *session) sendTalkerStart(tg uint32, callsign string) {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c != nil {
		if err := c.SendTalkerStart(tg, callsign); err != nil {
			log.Printf("SendTalkerStart error: %v", err)
		}
	}
}

func (s *session) sendTalkerStop(tg uint32, callsign string) {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c != nil {
		if err := c.SendTalkerStop(tg, callsign); err != nil {
			log.Printf("SendTalkerStop error: %v", err)
		}
	}
}

func (s *session) sendAudio(b64 string) {
	opusData, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		log.Printf("TX audio base64 decode error: %v", err)
		return
	}
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c != nil {
		if err := c.SendAudio(opusData); err != nil {
			log.Printf("SendAudio error: %v", err)
		}
	}
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
