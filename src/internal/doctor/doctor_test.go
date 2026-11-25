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

// A report made without a runtime reading claims nothing about the runtime.
// Host() does not measure it, and the dashboard's /api/v1/status served that
// report as "runtime": {"reachable": false}, a claim nobody measured, beside a
// runtime that answered every other reading on the page (2026-11-25).
func TestAnUnmeasuredRuntimeIsAbsentNotUnreachable(t *testing.T) {
	r := Host("v0.1.0", "abc", "/repo", "/home")
	var js strings.Builder
	if err := WriteJSON(&js, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(js.String(), `"runtime"`) || strings.Contains(js.String(), `"reachable"`) {
		t.Errorf("a host-only report claims a runtime state:\n%s", js.String())
	}
	var txt strings.Builder
	if err := WriteText(&txt, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(txt.String(), "NOT reachable") || !strings.Contains(txt.String(), "not measured") {
		t.Errorf("the text report of an unmeasured runtime:\n%s", txt.String())
	}
}

// A measured runtime is reported, reachable or not, as before.
func TestAMeasuredRuntimeIsReported(t *testing.T) {
	r := Host("v0.1.0", "abc", "/repo", "/home")
	rt := ParseRuntimeInfo("runtime: docker\nengine: NOT reachable\n")
	r.Runtime = &rt
	var js strings.Builder
	if err := WriteJSON(&js, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"reachable": false`) {
		t.Errorf("a measured, unreachable runtime is not reported:\n%s", js.String())
	}
	var txt strings.Builder
	if err := WriteText(&txt, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt.String(), "NOT reachable") {
		t.Errorf("the text report hides a measured, unreachable runtime:\n%s", txt.String())
	}
}
