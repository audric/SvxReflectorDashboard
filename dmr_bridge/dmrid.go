package main

import (
	"bufio"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

// dmrIDTable maps DMR radio IDs to callsigns, loaded once at startup from
// the radioid.net user database (baked into the image at build time).
var (
	dmrIDTable   map[uint32]string
	dmrIDTableMu sync.RWMutex
)

// loadDMRIDTable parses the radioid.net CSV (RADIO_ID,CALLSIGN,...) into
// an in-memory lookup table. Errors are logged but non-fatal: the bridge
// falls back to numeric "DMR-<id>" labels if the table is unavailable.
func loadDMRIDTable(path string) {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("[DMRID] Could not open %s: %v (callsign lookup disabled)", path, err)
		return
	}
	defer f.Close()

	table := make(map[uint32]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			continue // skip CSV header row
		}
		parts := strings.SplitN(line, ",", 3)
		if len(parts) < 2 {
			continue
		}
		id, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil {
			continue
		}
		cs := strings.TrimSpace(parts[1])
		if cs == "" {
			continue
		}
		table[uint32(id)] = cs
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[DMRID] Error reading %s: %v", path, err)
	}

	dmrIDTableMu.Lock()
	dmrIDTable = table
	dmrIDTableMu.Unlock()
	log.Printf("[DMRID] Loaded %d DMR ID -> callsign entries from %s", len(table), path)
}

// lookupDMRCallsign returns the callsign for a DMR radio ID, or a
// "DMR-<id>" fallback label if the ID isn't found (or the table hasn't
// loaded yet).
func lookupDMRCallsign(id uint32) string {
	dmrIDTableMu.RLock()
	cs, ok := dmrIDTable[id]
	dmrIDTableMu.RUnlock()
	if ok {
		return cs
	}
	return "DMR-" + strconv.FormatUint(uint64(id), 10)
}
