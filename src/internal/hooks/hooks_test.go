package hooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSignRoundTrip(t *testing.T) {
	body := []byte(`{"type":"job.succeeded"}`)
	ts := "1700000000"
	sig := Sign("dbphook_test", ts, body)
	if !Verify("dbphook_test", ts, "sha256="+sig, body, time.Unix(1700000000, 0)) {
		t.Fatal("valid signature rejected")
	}
	if Verify("wrong", ts, sig, body, time.Unix(1700000000, 0)) {
		t.Fatal("wrong secret accepted")
	}
}

func TestHTTPSOnlyExceptLoopback(t *testing.T) {
	if err := validateURL("http://example.com/hook"); err == nil {
		t.Fatal("plain http to the internet must be refused")
	}
	if err := validateURL("http://127.0.0.1:9/hook"); err != nil {
		t.Fatal(err)
	}
	if err := validateURL("https://hooks.example.com/x"); err != nil {
		t.Fatal(err)
	}
}

func TestDeliverSignedWebhook(t *testing.T) {
	type delivery struct {
		sig, ts, event string
		body           []byte
	}
	got := make(chan delivery, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- delivery{
			sig:   r.Header.Get("X-Dbpantry-Signature"),
			ts:    r.Header.Get("X-Dbpantry-Timestamp"),
			event: r.Header.Get("X-Dbpantry-Event"),
			body:  body,
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "hooks.json")
	b, err := New(path, 50)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := b.CreateEndpoint(srv.URL, []string{JobSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Secret == "" {
		t.Fatal("create must show the secret once")
	}
	b.Emit(context.Background(), Event{Type: JobSucceeded, Payload: map[string]any{"action": "verify"}})
	var d delivery
	select {
	case d = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
	}
	if d.event != JobSucceeded {
		t.Fatalf("event header = %q", d.event)
	}
	if !Verify(ep.Secret, d.ts, d.sig, d.body, time.Now()) {
		t.Fatalf("delivery signature invalid: %s body=%s", d.sig, d.body)
	}
	var decoded Event
	if err := json.Unmarshal(d.body, &decoded); err != nil || decoded.Type != JobSucceeded {
		t.Fatalf("body = %s err=%v", d.body, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var listed []Endpoint
	for time.Now().Before(deadline) {
		listed = b.ListEndpoints()
		if len(listed) == 1 && listed[0].Secret == "" && listed[0].LastStatus == 204 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("list = %+v", listed)
}

func TestWatchReceivesEmit(t *testing.T) {
	b, err := New("", 10)
	if err != nil {
		t.Fatal(err)
	}
	cancel := make(chan struct{})
	ch := b.Watch(cancel)
	b.Emit(context.Background(), Event{Type: AlertRaised, Severity: "error", Payload: map[string]any{"msg": "x"}})
	select {
	case e := <-ch:
		if e.Type != AlertRaised {
			t.Fatalf("%+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("watch timed out")
	}
	close(cancel)
}
