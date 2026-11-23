// Package netspace picks a Compose network subnet that does not overlap
// pools the container engine already holds (Docker "Pool overlaps with
// other one on this address space").
package netspace

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// DefaultSubnet is the shipped MDB_NETWORK_SUBNET (kit .env.example).
const DefaultSubnet = "172.28.0.0/16"

// FixtureSubnet is the shipped fixture default; stack auto-pick skips it so
// a later fixture up still has a free default (design.md D-103).
const FixtureSubnet = "172.29.0.0/16"

// Decision is the result of Ensure.
type Decision struct {
	// Subnet is the CIDR to use (wanted or a replacement).
	Subnet string
	// Changed is true when Subnet differs from the requested value.
	Changed bool
	// Reason explains a change (empty when unchanged).
	Reason string
	// Conflict is the first overlapping used CIDR when Changed.
	Conflict string
}

// Overlaps reports whether two IPv4 CIDRs share any address.
func Overlaps(a, b string) bool {
	_, x, errA := net.ParseCIDR(a)
	_, y, errB := net.ParseCIDR(b)
	if errA != nil || errB != nil {
		return false
	}
	return x.Contains(y.IP) || y.Contains(x.IP)
}

// Normalize returns a canonical IPv4 CIDR or an error.
func Normalize(cidr string) (string, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return DefaultSubnet, nil
	}
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil || n.IP.To4() == nil {
		return "", fmt.Errorf("network subnet %q is not an IPv4 CIDR", cidr)
	}
	ones, bits := n.Mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("network subnet %q is not IPv4", cidr)
	}
	return n.IP.Mask(n.Mask).String() + "/" + strconv.Itoa(ones), nil
}

// FirstHost is the subnet's first usable host (…x.1 for a typical /16), used
// as the Incus gateway default.
func FirstHost(cidr string) (string, error) {
	norm, err := Normalize(cidr)
	if err != nil {
		return "", err
	}
	_, n, err := net.ParseCIDR(norm)
	if err != nil {
		return "", err
	}
	ip := make(net.IP, len(n.IP))
	copy(ip, n.IP.To4())
	ip[3]++
	return ip.String(), nil
}

// Ensure picks want when it is free among used; otherwise the first free
// candidate. ownExists means the Compose project network is already created
// — leave want alone (Compose reuses the existing network).
// lock refuses to change want when it conflicts.
func Ensure(want string, used []string, ownExists, lock bool) (Decision, error) {
	want, err := Normalize(want)
	if err != nil {
		return Decision{}, err
	}
	if ownExists {
		return Decision{Subnet: want}, nil
	}
	clean := uniqueCIDRs(used)
	if conflict := firstConflict(want, clean); conflict == "" {
		return Decision{Subnet: want}, nil
	} else if lock {
		return Decision{}, fmt.Errorf("MDB_NETWORK_SUBNET=%s overlaps %s; set a free CIDR or unset MDB_NETWORK_SUBNET_LOCK", want, conflict)
	}
	next, err := Pick(clean, want)
	if err != nil {
		return Decision{}, err
	}
	return Decision{
		Subnet:   next,
		Changed:  next != want,
		Reason:   "configured subnet overlaps an existing engine network",
		Conflict: firstConflict(want, clean),
	}, nil
}

// Pick returns the first candidate /16 that does not overlap used (and skips
// the fixture default so both can coexist). prefer is tried first when free.
func Pick(used []string, prefer string) (string, error) {
	clean := uniqueCIDRs(used)
	if prefer != "" {
		if p, err := Normalize(prefer); err == nil && firstConflict(p, clean) == "" && !Overlaps(p, FixtureSubnet) {
			return p, nil
		}
	}
	for _, c := range candidates() {
		if firstConflict(c, clean) != "" {
			continue
		}
		if Overlaps(c, FixtureSubnet) {
			continue
		}
		return c, nil
	}
	return "", fmt.Errorf("no free IPv4 /16 left in 172.16.0.0/12 for MDB_NETWORK_SUBNET (used: %s)", strings.Join(clean, ", "))
}

func candidates() []string {
	out := make([]string, 0, 16)
	for i := 16; i <= 31; i++ {
		out = append(out, fmt.Sprintf("172.%d.0.0/16", i))
	}
	return out
}

func uniqueCIDRs(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		n, err := Normalize(s)
		if err != nil {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func firstConflict(want string, used []string) string {
	for _, u := range used {
		if Overlaps(want, u) {
			return u
		}
	}
	return ""
}

// GatewayInSubnet reports whether gw is inside cidr (for rewriting
// MDB_NETWORK_GATEWAY after an auto move).
func GatewayInSubnet(gw, cidr string) bool {
	ip := net.ParseIP(strings.TrimSpace(gw))
	if ip == nil || ip.To4() == nil {
		return false
	}
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return n.Contains(ip)
}
