package dashboard

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/simtabi/dbpantry/src/internal/hooks"
)

// The Operations page manages webhooks as it manages API tokens: listed to an
// admin only, as /api/v1/hooks is, with New and Delete; the signing secret is
// never on the page (it is shown once, from the create response). Webhooks
// had an API and no page (2026-11-25).
func TestOperationsManagesWebhooksForAdmins(t *testing.T) {
	m := newMgmt(t, AuthToken, Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, false)
	bus, err := hooks.New("", 200)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := bus.CreateEndpoint("https://hooks.example.test/dbpantry", []string{hooks.JobFailed})
	if err != nil {
		t.Fatal(err)
	}
	h, err := Handler(Sources{Snapshot: fixture, Project: showFixture}, Options{
		Auth: m.auth, Runner: m.runner, Settings: Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, Audit: m.audit, Hooks: bus,
		Choices: func(context.Context) (Choices, error) { return Choices{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	m.h = h
	read, _, err := m.tokens.Create("reader", ScopeRead, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	admin, _, err := m.tokens.Create("adminbot", ScopeAdmin, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	get := func(tok string) string {
		return m.do(call{method: http.MethodGet, path: "/operations", header: map[string]string{"Authorization": "Bearer " + tok}}).Body.String()
	}

	b := get(admin)
	for _, want := range []string{`id="webhooks"`, "hooks.example.test", `data-hook-delete="` + ep.ID + `"`, "data-hook-create", hooks.JobFailed} {
		if !strings.Contains(b, want) {
			t.Errorf("an admin's Operations page lacks %q", want)
		}
	}
	if strings.Contains(b, ep.Secret) {
		t.Error("the Operations page shows a webhook's signing secret")
	}
	if b := get(read); strings.Contains(b, "hooks.example.test") || strings.Contains(b, "data-hook-delete") {
		t.Error("a read token sees the webhooks on /operations")
	}

	// Without an event bus the card says webhooks are off, rather than
	// offering a form whose API would answer 404.
	h, err = Handler(Sources{Snapshot: fixture, Project: showFixture}, Options{
		Auth: m.auth, Runner: m.runner, Settings: Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, Audit: m.audit,
		Choices: func(context.Context) (Choices, error) { return Choices{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	m.h = h
	if b := get(admin); !strings.Contains(b, "Webhooks are off") || strings.Contains(b, "data-hook-create") {
		t.Error("with no event bus the page still offers to create a webhook")
	}
}
