package explog

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	mu   sync.Mutex
	f    *os.File
	on   bool
	host string
)

// Init abre o arquivo de eventos JSONL apontado por SYNCLI_EXP_LOG.
// Quando a variável está vazia ou o arquivo não pode ser aberto, Emit não faz nada.
func Init() {
	path := os.Getenv("SYNCLI_EXP_LOG")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "explog: open %s: %v\n", path, err)
		return
	}
	mu.Lock()
	f = file
	on = true
	host, _ = os.Hostname()
	mu.Unlock()
}

// Emit escreve uma linha JSON com timestamp em nanossegundos (CLOCK_REALTIME).
// event é um de: detect, send, apply, fs_event, debounce_out, ack_send,
// ack_recv, snapshot_req, snapshot_end, snapshot_serve_start,
// snapshot_serve_end, peer_discovered, peer_removed.
func Emit(event, path, op string, bytes int) {
	EmitFields(event, map[string]any{
		"path":  path,
		"op":    op,
		"bytes": bytes,
	})
}

// EmitFields escreve uma linha JSON com campos arbitrários. ts_ns, node e event
// são sempre adicionados automaticamente. O chamador passa quaisquer campos
// adicionais (ex.: bytes_wire, peer_id, blocks).
func EmitFields(event string, fields map[string]any) {
	if !on {
		return
	}
	rec := make(map[string]any, len(fields)+3)
	for k, v := range fields {
		rec[k] = v
	}
	rec["ts_ns"] = time.Now().UnixNano()
	rec["node"] = host
	rec["event"] = event
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	mu.Lock()
	f.Write(b)
	f.Write([]byte("\n"))
	mu.Unlock()
}
