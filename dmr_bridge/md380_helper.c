// md380_helper: persistent stdin/stdout AMBE+2 2450x1150 codec process
// using the real MD380 firmware via the md380_vocoder library. Must run
// as a 32-bit ARM binary (native or under qemu-arm), since the firmware
// is linked in as raw ARM machine code at fixed addresses.
//
// Protocol (binary, over stdin/stdout):
//   'D' + 9 bytes AMBE  -> 320 bytes PCM (160 x int16, host-endian)
//   'E' + 320 bytes PCM -> 9 bytes AMBE
#include <stdio.h>
#include <stdint.h>
#include <unistd.h>
#include "md380_vocoder.h"

static int read_full(int fd, void *buf, size_t n) {
	uint8_t *p = (uint8_t *)buf;
	size_t got = 0;
	while (got < n) {
		ssize_t r = read(fd, p + got, n - got);
		if (r <= 0) return -1;
		got += (size_t)r;
	}
	return 0;
}

static int write_full(int fd, const void *buf, size_t n) {
	const uint8_t *p = (const uint8_t *)buf;
	size_t sent = 0;
	while (sent < n) {
		ssize_t w = write(fd, p + sent, n - sent);
		if (w <= 0) return -1;
		sent += (size_t)w;
	}
	return 0;
}

int main(void) {
	if (md380_init() != 0) {
		fprintf(stderr, "md380_helper: md380_init failed\n");
		return 1;
	}

	uint8_t cmd;
	while (read_full(0, &cmd, 1) == 0) {
		if (cmd == 'D') {
			uint8_t ambe[9];
			int16_t pcm[160];
			if (read_full(0, ambe, sizeof(ambe)) != 0) break;
			md380_decode_fec(ambe, pcm);
			if (write_full(1, pcm, sizeof(pcm)) != 0) break;
		} else if (cmd == 'E') {
			int16_t pcm[160];
			uint8_t ambe[9] = {0};
			if (read_full(0, pcm, sizeof(pcm)) != 0) break;
			md380_encode_fec(ambe, pcm);
			if (write_full(1, ambe, sizeof(ambe)) != 0) break;
		} else {
			fprintf(stderr, "md380_helper: unknown command 0x%02x\n", cmd);
			break;
		}
	}
	return 0;
}
