package netspace

import (
	"strconv"
	"testing"
)

func TestEnsureKeepsFreeWant(t *testing.T) {
	d, err := Ensure("172.28.0.0/16", []string{"172.29.0.0/16", "192.168.0.0/24"}, false, false)
	if err != nil || d.Changed || d.Subnet != "172.28.0.0/16" {
		t.Fatalf("got %+v err=%v", d, err)
	}
}

func TestEnsureRepicksOnOverlap(t *testing.T) {
	d, err := Ensure("172.28.0.0/16", []string{"172.28.0.0/16", "172.29.0.0/16"}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Changed || d.Subnet == "172.28.0.0/16" || Overlaps(d.Subnet, "172.28.0.0/16") || Overlaps(d.Subnet, FixtureSubnet) {
		t.Fatalf("expected a free non-fixture subnet, got %+v", d)
	}
	if d.Conflict != "172.28.0.0/16" {
		t.Fatalf("conflict %q", d.Conflict)
	}
}

func TestEnsureLockRefuses(t *testing.T) {
	_, err := Ensure("172.28.0.0/16", []string{"172.28.0.0/16"}, false, true)
	if err == nil {
		t.Fatal("lock should refuse an overlap")
	}
}

func TestEnsureOwnNetworkSkips(t *testing.T) {
	d, err := Ensure("172.28.0.0/16", []string{"172.28.0.0/16"}, true, false)
	if err != nil || d.Changed || d.Subnet != "172.28.0.0/16" {
		t.Fatalf("existing project network must keep want: %+v err=%v", d, err)
	}
}

func TestPickSkipsFixtureDefault(t *testing.T) {
	// Fill every /16 except leave 172.29 free — Pick must still skip fixture.
	var used []string
	for i := 16; i <= 31; i++ {
		if i == 16 || i == 29 {
			continue
		}
		used = append(used, "172."+strconv.Itoa(i)+".0.0/16")
	}
	got, err := Pick(used, "172.28.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if got != "172.16.0.0/16" {
		t.Fatalf("got %s", got)
	}
	if Overlaps(got, FixtureSubnet) {
		t.Fatal("must not pick fixture default")
	}
}

func TestFirstHost(t *testing.T) {
	h, err := FirstHost("172.30.0.0/16")
	if err != nil || h != "172.30.0.1" {
		t.Fatalf("%s %v", h, err)
	}
}
