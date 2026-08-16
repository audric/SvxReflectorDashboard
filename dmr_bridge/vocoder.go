package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// PCM audio parameters
const (
	PCMSampleRate = 8000 // 8 kHz
	PCMFrameSize  = 160  // 160 samples = 20ms at 8kHz
	AMBEFrameSize = 9    // 9 bytes = 72 bits DMR AMBE+2
)

// Vocoder wraps a persistent md380_helper subprocess (the real MD380
// radio firmware running under qemu-arm emulation) for DMR AMBE+2
// 2450x1150 encode/decode. The subprocess speaks a simple framed
// protocol over stdin/stdout: 'D'+9 bytes AMBE -> 320 bytes PCM,
// 'E'+320 bytes PCM -> 9 bytes AMBE. Requests are serialized by mu,
// matching the subprocess's single request-in-flight design.
type Vocoder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
}

// NewVocoder starts the md380_helper subprocess (runs under qemu-arm via
// the host's registered binfmt_misc handler; no qemu binary needed in
// this container).
func NewVocoder() (*Vocoder, error) {
	cmd := exec.Command("/usr/local/bin/md380_helper")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("md380_helper stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("md380_helper stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("md380_helper start: %w", err)
	}
	return &Vocoder{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// Close terminates the subprocess.
func (v *Vocoder) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stdin != nil {
		v.stdin.Close()
	}
	if v.cmd != nil && v.cmd.Process != nil {
		v.cmd.Process.Kill()
		v.cmd.Wait()
	}
}

// Decode converts a 9-byte DMR AMBE+2 frame to 160 PCM samples (8kHz, 16-bit mono).
func (v *Vocoder) Decode(ambe [9]byte) [PCMFrameSize]int16 {
	v.mu.Lock()
	defer v.mu.Unlock()

	var pcm [PCMFrameSize]int16
	req := make([]byte, 1+9)
	req[0] = 'D'
	copy(req[1:], ambe[:])
	if _, err := v.stdin.Write(req); err != nil {
		return pcm
	}
	buf := make([]byte, PCMFrameSize*2)
	if _, err := io.ReadFull(v.stdout, buf); err != nil {
		return pcm
	}
	for i := 0; i < PCMFrameSize; i++ {
		pcm[i] = int16(binary.LittleEndian.Uint16(buf[i*2 : i*2+2]))
	}
	return pcm
}

// Encode converts 160 PCM samples (8kHz, 16-bit mono) to a 9-byte DMR AMBE+2 frame.
func (v *Vocoder) Encode(pcm [PCMFrameSize]int16) [9]byte {
	v.mu.Lock()
	defer v.mu.Unlock()

	var ambe [9]byte
	req := make([]byte, 1+PCMFrameSize*2)
	req[0] = 'E'
	for i := 0; i < PCMFrameSize; i++ {
		binary.LittleEndian.PutUint16(req[1+i*2:1+i*2+2], uint16(pcm[i]))
	}
	if _, err := v.stdin.Write(req); err != nil {
		return ambe
	}
	buf := make([]byte, 9)
	if _, err := io.ReadFull(v.stdout, buf); err != nil {
		return ambe
	}
	copy(ambe[:], buf)
	return ambe
}
