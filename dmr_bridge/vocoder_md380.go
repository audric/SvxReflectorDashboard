package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// md380Vocoder wraps a persistent md380_helper subprocess (the MD380
// radio firmware running under qemu-arm emulation) for DMR AMBE+2
// 2450x1150 encode/decode. The firmware is not part of the image: the
// helper maps the two image files supplied by the operator. The subprocess
// speaks a simple framed protocol over stdin/stdout: 'D'+9 bytes AMBE ->
// 320 bytes PCM, 'E'+320 bytes PCM -> 9 bytes AMBE. Requests are
// serialized by mu, matching the subprocess's single request-in-flight
// design.
type md380Vocoder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
	armed  bool
	closed bool
	exited chan struct{}
}

// newMD380Vocoder starts the md380_helper subprocess under the
// qemu-arm-static emulator shipped in the image (the host needs no qemu or
// binfmt_misc setup) and runs one frame through it. Any failure is
// returned so the caller can fall back to the built-in vocoder.
func newMD380Vocoder(fwPath, ramPath string) (*md380Vocoder, error) {
	cmd := exec.Command("/usr/local/bin/qemu-arm-static", "/usr/local/bin/md380_helper", fwPath, ramPath)
	cmd.Stderr = os.Stderr
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
	v := &md380Vocoder{cmd: cmd, stdin: stdin, stdout: stdout, exited: make(chan struct{})}
	// Once the self-test has passed, exit as soon as the helper goes away,
	// even while idle, so the container restarts with a working vocoder.
	go func() {
		err := cmd.Wait()
		v.mu.Lock()
		fatal := v.armed && !v.closed
		v.mu.Unlock()
		close(v.exited)
		if fatal {
			helperDied(fmt.Errorf("process exited: %v", err))
		}
	}()

	tested := make(chan error, 1)
	go func() {
		req := make([]byte, 1+PCMFrameSize*2)
		req[0] = 'E'
		if _, err := stdin.Write(req); err != nil {
			tested <- err
			return
		}
		_, err := io.ReadFull(stdout, make([]byte, AMBEFrameSize))
		tested <- err
	}()
	select {
	case err = <-tested:
	case <-v.exited:
		err = errors.New("helper exited during start-up")
	case <-time.After(10 * time.Second):
		err = errors.New("helper did not answer within 10s")
	}
	if err != nil {
		v.Close()
		return nil, fmt.Errorf("md380_helper self-test: %w", err)
	}
	v.mu.Lock()
	v.armed = true
	v.mu.Unlock()
	return v, nil
}

// Close terminates the subprocess.
func (v *md380Vocoder) Close() {
	v.mu.Lock()
	v.closed = true
	if v.stdin != nil {
		v.stdin.Close()
	}
	if v.cmd != nil && v.cmd.Process != nil {
		v.cmd.Process.Kill()
	}
	v.mu.Unlock()
	<-v.exited
}

// Decode converts a 9-byte DMR AMBE+2 frame to 160 PCM samples (8kHz, 16-bit mono).
func (v *md380Vocoder) Decode(ambe [9]byte) [PCMFrameSize]int16 {
	v.mu.Lock()
	defer v.mu.Unlock()

	var pcm [PCMFrameSize]int16
	req := make([]byte, 1+9)
	req[0] = 'D'
	copy(req[1:], ambe[:])
	if _, err := v.stdin.Write(req); err != nil {
		helperDied(err)
	}
	buf := make([]byte, PCMFrameSize*2)
	if _, err := io.ReadFull(v.stdout, buf); err != nil {
		helperDied(err)
	}
	for i := 0; i < PCMFrameSize; i++ {
		pcm[i] = int16(binary.LittleEndian.Uint16(buf[i*2 : i*2+2]))
	}
	return pcm
}

// Encode converts 160 PCM samples (8kHz, 16-bit mono) to a 9-byte DMR AMBE+2 frame.
func (v *md380Vocoder) Encode(pcm [PCMFrameSize]int16) [9]byte {
	v.mu.Lock()
	defer v.mu.Unlock()

	var ambe [9]byte
	req := make([]byte, 1+PCMFrameSize*2)
	req[0] = 'E'
	for i := 0; i < PCMFrameSize; i++ {
		binary.LittleEndian.PutUint16(req[1+i*2:1+i*2+2], uint16(pcm[i]))
	}
	if _, err := v.stdin.Write(req); err != nil {
		helperDied(err)
	}
	buf := make([]byte, 9)
	if _, err := io.ReadFull(v.stdout, buf); err != nil {
		helperDied(err)
	}
	copy(ambe[:], buf)
	return ambe
}

// helperDied exits the bridge when the vocoder subprocess is gone: without
// it every frame would be silence, so let the container restart instead.
func helperDied(err error) {
	log.Fatalf("[Vocoder] md380_helper stopped responding: %v", err)
}
