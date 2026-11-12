// md380_helper: persistent stdin/stdout AMBE+2 2450x1150 codec process
// using the MD380 firmware via the md380_vocoder library. Must run as a
// 32-bit ARM binary (native or under qemu-arm).
//
// The firmware is NOT linked in. The two image files are supplied by the
// operator and mapped at run time at the addresses the firmware expects:
//   md380_helper <md380fw.img> <md380ram.img>
//
// Protocol (binary, over stdin/stdout):
//   'D' + 9 bytes AMBE  -> 320 bytes PCM (160 x int16, host-endian)
//   'E' + 320 bytes PCM -> 9 bytes AMBE
#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <stdint.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>
#include "md380_vocoder.h"

#define FW_ADDR  0x0800C000u
#define FW_SIZE  0xf2c00
#define RAM_ADDR 0x20000000u
#define RAM_SIZE 0x20000

// Maps an image file privately at a fixed address, refusing to replace
// anything already mapped there.
static int map_image(const char *path, uintptr_t addr, size_t size, int prot) {
	int fd = open(path, O_RDONLY);
	if (fd < 0) {
		perror(path);
		return -1;
	}
	struct stat st;
	if (fstat(fd, &st) != 0 || (size_t)st.st_size != size) {
		fprintf(stderr, "md380_helper: %s: expected %zu bytes\n", path, size);
		close(fd);
		return -1;
	}
	void *p = mmap((void *)addr, size, prot, MAP_PRIVATE | MAP_FIXED_NOREPLACE, fd, 0);
	close(fd);
	if (p != (void *)addr) {
		fprintf(stderr, "md380_helper: cannot map %s at 0x%08lx\n", path, (unsigned long)addr);
		return -1;
	}
	return 0;
}

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

int main(int argc, char **argv) {
	if (argc != 3) {
		fprintf(stderr, "usage: md380_helper <md380fw.img> <md380ram.img>\n");
		return 2;
	}
	if (map_image(argv[1], FW_ADDR, FW_SIZE, PROT_READ | PROT_EXEC) != 0 ||
	    map_image(argv[2], RAM_ADDR, RAM_SIZE, PROT_READ | PROT_WRITE) != 0) {
		return 1;
	}
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
