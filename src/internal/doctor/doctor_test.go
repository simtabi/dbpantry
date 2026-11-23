package doctor

import (
	"strings"
	"testing"
)

func TestParseRuntimeInfoOrbStack(t *testing.T) {
	raw := `runtime: docker (provided by OrbStack)
cli: /usr/local/bin/docker
compose: docker compose [compose-v2]
engine: reachable (provided by OrbStack)
rootless: no
`
	rt := ParseRuntimeInfo(raw)
	if rt.Name != "docker" || rt.Provider != "OrbStack" || !rt.Reachable {
		t.Fatalf("got %+v", rt)
	}
	if rt.Flavor != "compose-v2" || !strings.Contains(rt.Compose, "docker compose") {
		t.Fatalf("compose = %q flavor = %q", rt.Compose, rt.Flavor)
	}
}

func TestHostFillsMachineFacts(t *testing.T) {
	r := Host("v0.1.0", "abc", "/repo", "/home")
	if r.Version != "v0.1.0" || r.Root != "/repo" || r.NumCPU < 1 {
		t.Fatalf("got %+v", r)
	}
}
