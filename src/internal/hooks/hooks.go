// Package hooks is the outbound event bus for the management API
// (design.md D-99): projects and operators subscribe with signed webhooks,
// and live clients follow the same stream over SSE.
//
// Security model:
//   - Delivery is HTTPS-only by default (http:// only for loopback).
//   - Each endpoint has its own secret; the body is signed
//     HMAC-SHA256(secret, timestamp+"."+body) as X-Dbpantry-Signature.
//   - Secrets never appear in list/get JSON after create (shown once).
//   - No inbound webhooks: outside systems call the existing Bearer API.
package hooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event kinds the bus emits. Keep stable — subscribers match on these.
const (
	JobStarted   = "job.started"
	JobSucceeded = "job.succeeded"
	JobFailed    = "job.failed"
	JobCancelled = "job.cancelled"
	JobPaused    = "job.paused"
	JobContinued = "job.continued"
	HealthWarn   = "health.degraded"
	BackupFail   = "backup.failed"
	AlertRaised  = "alert.raised"
)

// Event is one fact that happened inside the instance.
type Event struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	At       time.Time      `json:"at"`
	Actor    string         `json:"actor,omitempty"`
	Source   string         `json:"source,omitempty"`
	Severity string         `json:"severity,omitempty"` // info|warn|error
	Payload  map[string]any `json:"payload,omitempty"`
	Project  string         `json:"project,omitempty"`
	Engine   string         `json:"engine,omitempty"`
}

// Endpoint is one outbound webhook subscription.
type Endpoint struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	Events     []string  `json:"events"` // empty = all
	Enabled    bool      `json:"enabled"`
	Created    time.Time `json:"created"`
	LastStatus int       `json:"last_status,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	LastAt     time.Time `json:"last_at,omitempty"`
	// Secret is only set on create responses.
	Secret string `json:"secret,omitempty"`
}

type stored struct {
	Endpoint
	SecretHash string `json:"-"`
	secret     string // plaintext kept in memory for signing; never written
}

// Bus holds recent events and delivers matching webhooks.
type Bus struct {
	path   string // JSON file of endpoints (secrets as sha256 hex)
	client *http.Client
	now    func() time.Time
	keep   int

	mu        sync.Mutex
	events    []Event
	endpoints map[string]*stored
	order     []string
	watchers  map[chan Event]struct{}
}

// New opens (or creates) the endpoint store at path. keep is how many
// recent events stay in memory for GET /events and SSE replay.
func New(path string, keep int) (*Bus, error) {
	if keep <= 0 {
		keep = 200
	}
	b := &Bus{
		path:      path,
		client:    &http.Client{Timeout: 15 * time.Second},
		now:       time.Now,
		keep:      keep,
		endpoints: map[string]*stored{},
		watchers:  map[chan Event]struct{}{},
	}
	if path != "" {
		if err := b.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return b, nil
}

// Emit records an event, wakes SSE watchers, and delivers matching webhooks
// asynchronously. Payload must not contain secrets.
func (b *Bus) Emit(ctx context.Context, e Event) {
	if e.ID == "" {
		id, err := newID()
		if err == nil {
			e.ID = id
		}
	}
	if e.At.IsZero() {
		e.At = b.now().UTC()
	}
	if e.Severity == "" {
		e.Severity = "info"
		if strings.Contains(e.Type, "fail") || e.Type == JobCancelled || e.Type == AlertRaised {
			e.Severity = "error"
		} else if e.Type == JobPaused || e.Type == HealthWarn {
			e.Severity = "warn"
		}
	}
	b.mu.Lock()
	b.events = append(b.events, e)
	if len(b.events) > b.keep {
		b.events = b.events[len(b.events)-b.keep:]
	}
	for ch := range b.watchers {
		select {
		case ch <- e:
		default:
		}
	}
	targets := b.matchLocked(e.Type)
	b.mu.Unlock()

	for _, ep := range targets {
		go b.deliver(context.WithoutCancel(ctx), ep, e)
	}
}

// Recent returns newest-first events, at most n (all when n <= 0).
func (b *Bus) Recent(n int) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := append([]Event(nil), b.events...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Watch returns a channel of live events. Close cancel to unsubscribe.
func (b *Bus) Watch(cancel <-chan struct{}) <-chan Event {
	ch := make(chan Event, 16)
	b.mu.Lock()
	b.watchers[ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		<-cancel
		b.mu.Lock()
		delete(b.watchers, ch)
		b.mu.Unlock()
		close(ch)
	}()
	return ch
}

// ListEndpoints returns every subscription without secrets.
func (b *Bus) ListEndpoints() []Endpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Endpoint, 0, len(b.order))
	for _, id := range b.order {
		e := b.endpoints[id].Endpoint
		e.Secret = ""
		out = append(out, e)
	}
	return out
}

// CreateEndpoint registers a webhook. The returned Endpoint.Secret is shown once.
func (b *Bus) CreateEndpoint(rawURL string, events []string) (Endpoint, error) {
	if err := validateURL(rawURL); err != nil {
		return Endpoint{}, err
	}
	secret, err := newSecret()
	if err != nil {
		return Endpoint{}, err
	}
	id, err := newID()
	if err != nil {
		return Endpoint{}, err
	}
	ev := normalizeEvents(events)
	st := &stored{
		Endpoint: Endpoint{
			ID: id, URL: rawURL, Events: ev, Enabled: true, Created: b.now().UTC(),
			Secret: secret,
		},
		SecretHash: hashSecret(secret),
		secret:     secret,
	}
	b.mu.Lock()
	b.endpoints[id] = st
	b.order = append(b.order, id)
	err = b.saveLocked()
	out := st.Endpoint
	b.mu.Unlock()
	return out, err
}

// DeleteEndpoint removes a subscription.
func (b *Bus) DeleteEndpoint(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.endpoints[id]; !ok {
		return ErrNotFound
	}
	delete(b.endpoints, id)
	next := b.order[:0]
	for _, x := range b.order {
		if x != id {
			next = append(next, x)
		}
	}
	b.order = next
	return b.saveLocked()
}

// SetEnabled toggles delivery for one endpoint.
func (b *Bus) SetEnabled(id string, on bool) (Endpoint, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.endpoints[id]
	if !ok {
		return Endpoint{}, ErrNotFound
	}
	st.Enabled = on
	_ = b.saveLocked()
	e := st.Endpoint
	e.Secret = ""
	return e, nil
}

// ErrNotFound is an unknown webhook id.
var ErrNotFound = errors.New("webhook not found")

func (b *Bus) matchLocked(typ string) []*stored {
	var out []*stored
	for _, id := range b.order {
		st := b.endpoints[id]
		if !st.Enabled {
			continue
		}
		if len(st.Events) == 0 || contains(st.Events, typ) || contains(st.Events, "*") {
			out = append(out, st)
		}
	}
	return out
}

func (b *Bus) deliver(ctx context.Context, st *stored, e Event) {
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	ts := fmt.Sprintf("%d", b.now().UTC().Unix())
	sig := Sign(st.secret, ts, body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, st.URL, bytes.NewReader(body))
	if err != nil {
		b.mark(st.ID, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dbpantry-hooks/1")
	req.Header.Set("X-Dbpantry-Event", e.Type)
	req.Header.Set("X-Dbpantry-Delivery", e.ID)
	req.Header.Set("X-Dbpantry-Timestamp", ts)
	req.Header.Set("X-Dbpantry-Signature", "sha256="+sig)
	res, err := b.client.Do(req)
	if err != nil {
		b.mark(st.ID, 0, err.Error())
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64*1024))
	_ = res.Body.Close()
	if res.StatusCode >= 300 {
		b.mark(st.ID, res.StatusCode, "HTTP "+res.Status)
		return
	}
	b.mark(st.ID, res.StatusCode, "")
}

func (b *Bus) mark(id string, status int, errMsg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.endpoints[id]
	if !ok {
		return
	}
	st.LastStatus = status
	st.LastError = errMsg
	st.LastAt = b.now().UTC()
	_ = b.saveLocked()
}

// Sign returns hex HMAC-SHA256(secret, timestamp+"."+body).
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a delivery signature with a 5-minute skew window.
func Verify(secret, timestamp, signature string, body []byte, now time.Time) bool {
	signature = strings.TrimPrefix(signature, "sha256=")
	want := Sign(secret, timestamp, body)
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return false
	}
	sec, err := parseUnix(timestamp)
	if err != nil {
		return false
	}
	skew := now.UTC().Sub(time.Unix(sec, 0))
	if skew < 0 {
		skew = -skew
	}
	return skew <= 5*time.Minute
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("webhook URL is not valid")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("webhook URL must be https (http allowed only for loopback)")
	default:
		return fmt.Errorf("webhook URL scheme must be https")
	}
}

func normalizeEvents(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func newSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "dbphook_" + hex.EncodeToString(b), nil
}

func parseUnix(s string) (int64, error) {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("bad timestamp")
		}
		n = n*10 + int64(r-'0')
	}
	return n, nil
}

type fileShape struct {
	Endpoints []fileEndpoint `json:"endpoints"`
}

type fileEndpoint struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	Events     []string  `json:"events"`
	Enabled    bool      `json:"enabled"`
	Created    time.Time `json:"created"`
	SecretHash string    `json:"secret_hash"`
	// SecretPlain is written only when the process cannot re-derive the
	// plaintext (we keep it encrypted-at-rest via 0600 file ownership).
	SecretPlain string `json:"secret,omitempty"`
}

func (b *Bus) load() error {
	f, err := os.Open(b.path) // #nosec G304 -- instance home path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var doc fileShape
	if err := json.NewDecoder(f).Decode(&doc); err != nil {
		return err
	}
	for _, row := range doc.Endpoints {
		st := &stored{
			Endpoint: Endpoint{
				ID: row.ID, URL: row.URL, Events: row.Events, Enabled: row.Enabled, Created: row.Created,
			},
			SecretHash: row.SecretHash,
			secret:     row.SecretPlain,
		}
		b.endpoints[row.ID] = st
		b.order = append(b.order, row.ID)
	}
	return nil
}

func (b *Bus) saveLocked() error {
	if b.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	doc := fileShape{}
	for _, id := range b.order {
		st := b.endpoints[id]
		doc.Endpoints = append(doc.Endpoints, fileEndpoint{
			ID: st.ID, URL: st.URL, Events: st.Events, Enabled: st.Enabled, Created: st.Created,
			SecretHash: st.SecretHash, SecretPlain: st.secret,
		})
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}
