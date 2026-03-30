package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RedisClient is a minimal RESP client supporting SELECT, SETEX, and DEL.
// No external dependencies — uses raw TCP with the RESP protocol.
type RedisClient struct {
	addr string
	db   int
	conn net.Conn
	mu   sync.Mutex
}

// ParseRedisURL parses a redis:// URL and returns a RedisClient.
// Format: redis://host:port/db
func ParseRedisURL(rawURL string) (*RedisClient, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		host = "localhost"
	}
	port := u.Port()
	if port == "" {
		port = "6379"
	}

	db := 0
	if len(u.Path) > 1 {
		db, err = strconv.Atoi(strings.TrimPrefix(u.Path, "/"))
		if err != nil {
			return nil, fmt.Errorf("invalid redis DB number: %w", err)
		}
	}

	return &RedisClient{
		addr: net.JoinHostPort(host, port),
		db:   db,
	}, nil
}

// Connect establishes the TCP connection and selects the database.
func (r *RedisClient) Connect() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	conn, err := net.DialTimeout("tcp", r.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	r.conn = conn

	if r.db != 0 {
		if err := r.sendCommandLocked("SELECT", strconv.Itoa(r.db)); err != nil {
			conn.Close()
			return fmt.Errorf("redis SELECT: %w", err)
		}
		if _, err := r.readLineLocked(); err != nil {
			conn.Close()
			return fmt.Errorf("redis SELECT response: %w", err)
		}
	}

	return nil
}

// SetEX sets a key with a TTL in seconds.
func (r *RedisClient) SetEX(key string, seconds int, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("redis not connected")
	}
	if err := r.sendCommandLocked("SETEX", key, strconv.Itoa(seconds), value); err != nil {
		return err
	}
	_, err := r.readLineLocked()
	return err
}

// Del deletes a key.
func (r *RedisClient) Del(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("redis not connected")
	}
	if err := r.sendCommandLocked("DEL", key); err != nil {
		return err
	}
	_, err := r.readLineLocked()
	return err
}

// Close closes the connection.
func (r *RedisClient) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
	}
}

// sendCommandLocked writes a RESP array command. Caller must hold mu.
func (r *RedisClient) sendCommandLocked(args ...string) error {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, arg := range args {
		buf.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}
	r.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := r.conn.Write([]byte(buf.String()))
	return err
}

// readLineLocked reads a single RESP response line. Caller must hold mu.
func (r *RedisClient) readLineLocked() (string, error) {
	r.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 256)
	n, err := r.conn.Read(buf)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf[:n])), nil
}
