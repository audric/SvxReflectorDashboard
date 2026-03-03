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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("SVX Audio Bridge starting...")

	// Read configuration from environment
	host := envRequired("REFLECTOR_HOST")
	port := envInt("REFLECTOR_PORT", 5300)
	authKey := envDefault("REFLECTOR_AUTH_KEY", "")
	callsign := envDefault("AUDIO_CALLSIGN", "MONITOR")
	defaultTG := envInt("DEFAULT_TG", 1)
	redisURL := envDefault("REDIS_URL", "redis://redis:6379/1")

	log.Printf("Config: host=%s port=%d callsign=%s defaultTG=%d", host, port, callsign, defaultTG)

	// Connect to Redis
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

	// Track current TG for publishing
	var currentTG atomic.Uint32
	currentTG.Store(uint32(defaultTG))

	// Reconnect loop
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	backoff := 5 * time.Second
	resetBackoff := func() { backoff = 5 * time.Second }
	for {
		err := runClient(ctx, rdb, host, port, authKey, callsign, &currentTG, sigCh, resetBackoff)
		if err == errShutdown {
			log.Println("Shutting down gracefully")
			rdb.Close()
			return
		}
		log.Printf("Client disconnected: %v — reconnecting in %v...", err, backoff)
		time.Sleep(backoff)
		// Exponential backoff up to 60s for repeated failures (e.g. auth denied)
		if backoff < 60*time.Second {
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		}
	}
}

var errShutdown = fmt.Errorf("shutdown requested")

func runClient(ctx context.Context, rdb *redis.Client, host string, port int, authKey, callsign string, currentTG *atomic.Uint32, sigCh chan os.Signal, resetBackoff func()) error {
	client := NewClient(host, port, authKey, callsign)

	// Connect and authenticate
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	resetBackoff() // Connection succeeded, reset backoff
	defer client.Close()

	// Select initial TG
	tg := currentTG.Load()
	if err := client.SelectTG(tg); err != nil {
		return fmt.Errorf("select TG: %w", err)
	}

	// Set audio callback: publish OPUS frames to Redis via ActionCable-compatible broadcast
	client.SetAudioCallback(func(opusFrame []byte) {
		tg := currentTG.Load()
		channel := fmt.Sprintf("audio:tg:%d", tg)

		// Encode as base64 for JSON transport
		encoded := base64.StdEncoding.EncodeToString(opusFrame)

		// Publish via Redis in ActionCable-compatible format
		// ActionCable with Redis adapter subscribes to channel names directly
		msg := map[string]interface{}{
			"audio": encoded,
			"seq":   time.Now().UnixMilli(),
		}
		payload, _ := json.Marshal(msg)
		rdb.Publish(ctx, channel, payload)
	})

	// Start all goroutines
	errCh := make(chan error, 4)

	go func() {
		client.RunTCPReader()
		errCh <- fmt.Errorf("TCP reader stopped")
	}()

	go func() {
		client.RunTCPHeartbeat()
		errCh <- fmt.Errorf("TCP heartbeat stopped")
	}()

	go func() {
		client.RunUDPReader()
		errCh <- fmt.Errorf("UDP reader stopped")
	}()

	go func() {
		client.RunUDPHeartbeat()
		errCh <- fmt.Errorf("UDP heartbeat stopped")
	}()

	// Listen for TG switch commands from Redis
	go func() {
		sub := rdb.Subscribe(ctx, "audio:commands")
		defer sub.Close()

		ch := sub.Channel()
		for msg := range ch {
			var cmd struct {
				Action string `json:"action"`
				TG     int    `json:"tg"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				log.Printf("Invalid command: %v", err)
				continue
			}

			if cmd.Action == "select_tg" && cmd.TG > 0 {
				newTG := uint32(cmd.TG)
				currentTG.Store(newTG)
				if err := client.SelectTG(newTG); err != nil {
					log.Printf("SelectTG error: %v", err)
				}
			}
		}
	}()

	// Wait for shutdown signal or error
	select {
	case sig := <-sigCh:
		log.Printf("Received signal: %v", sig)
		return errShutdown
	case err := <-errCh:
		return err
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
