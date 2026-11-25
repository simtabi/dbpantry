package dashboard

import (
	"strings"
	"testing"
)

// "Add the prod overlay" runs `dbpantry config prod-overlay` on the
// instance's own .env: admin scope, because it changes what the stack boots,
// and no parameter, so a browser never names a file to edit (2026-11-25).
func TestProdOverlayIsAnAdminActionOnTheInstanceEnv(t *testing.T) {
	a, ok := actionByName("config.prod-overlay")
	if !ok {
		t.Fatal("no config.prod-overlay action")
	}
	if a.Scope != ScopeAdmin {
		t.Errorf("scope = %v, want admin", a.Scope)
	}
	if len(a.Params) != 0 {
		t.Errorf("params = %v, want none: the instance's .env only", a.Params)
	}
	v, err := a.validate(map[string]string{}, Choices{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(a.Command(v), " "); got != "config prod-overlay" {
		t.Errorf("command = %q, want config prod-overlay", got)
	}
	if _, err := a.validate(map[string]string{"file": "/etc/passwd"}, Choices{}); err == nil {
		t.Error("an unknown parameter (a file path) was accepted")
	}
}
