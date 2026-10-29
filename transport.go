package watchtower

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/watchtowerobservability/watchtower-go/internal/wal"
)

var errUnavailable = errors.New("watchtower: gateway still unavailable")

// bufferedTransport sends requests normally. When the gateway is unreachable or answers
// 429/5xx, it stores the request in the WAL and reports success to the exporter (so the SDK
// does not retry in memory); the next successful send replays the stored requests oldest first.
// The ingest key is never written to disk: it is added again on replay.
type bufferedTransport struct {
	next http.RoundTripper
	key  string
	w    *wal.WAL

	mu      sync.Mutex // serializes replay
	pending bool       // the WAL may hold records
}

type record struct {
	URL      string `json:"u"`
	Type     string `json:"t"`
	Encoding string `json:"e,omitempty"`
	Body     []byte `json:"b"`
}

func newTransport(next http.RoundTripper, key string, w *wal.WAL) *bufferedTransport {
	if next == nil {
		next = http.DefaultTransport
	}
	// a WAL opened over leftovers from an earlier run is replayed on the first success
	return &bufferedTransport{next: next, key: key, w: w, pending: true}
}

func failed(resp *http.Response, err error) bool {
	return err != nil || resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
}

func (t *bufferedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	rec := record{URL: req.URL.String(), Type: req.Header.Get("Content-Type"), Encoding: req.Header.Get("Content-Encoding"), Body: body}
	resp, err := t.send(req.Context(), rec)
	if !failed(resp, err) {
		t.replay()
		return resp, nil
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	raw, _ := json.Marshal(rec)
	if werr := t.w.Append(raw); werr != nil {
		return nil, werr
	}
	t.mu.Lock()
	t.pending = true
	t.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{},
		Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
}

func (t *bufferedTransport) send(ctx context.Context, r record) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", r.Type)
	if r.Encoding != "" {
		req.Header.Set("Content-Encoding", r.Encoding)
	}
	req.Header.Set("Authorization", "Bearer "+t.key)
	return t.next.RoundTrip(req)
}

// replay resends stored requests; it stops at the first failure and keeps that segment.
func (t *bufferedTransport) replay() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.pending {
		return
	}
	err := t.w.Replay(func(raw []byte) error {
		var r record
		if json.Unmarshal(raw, &r) != nil {
			return nil // unreadable record: drop it
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := t.send(ctx, r)
		if failed(resp, err) {
			if resp != nil {
				_ = resp.Body.Close()
			}
			return errUnavailable
		}
		_ = resp.Body.Close()
		return nil // 2xx, or a 4xx the gateway will never accept: drop
	})
	t.pending = err != nil
}
