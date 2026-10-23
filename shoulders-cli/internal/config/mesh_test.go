package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestMeshDefaultsAreOptIn(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MeshEnabled() {
		t.Fatal("mesh must be disabled by default so monocluster behaviour is unchanged")
	}
	if cfg.MeshNetwork() != DefaultMeshNetwork {
		t.Fatalf("unexpected default network %q", cfg.MeshNetwork())
	}
	if cfg.MeshPodCIDRBase() != DefaultMeshPodCIDRBase {
		t.Fatalf("unexpected default pod CIDR base %d", cfg.MeshPodCIDRBase())
	}
}

func TestMeshOverrides(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Platform.Mesh = MeshConfig{Enabled: true, Network: "custom-net", PodCIDRBase: 200}
	if !cfg.MeshEnabled() {
		t.Fatal("expected mesh enabled")
	}
	if cfg.MeshNetwork() != "custom-net" {
		t.Fatalf("got %q", cfg.MeshNetwork())
	}
	if cfg.MeshPodCIDRBase() != 200 {
		t.Fatalf("got %d", cfg.MeshPodCIDRBase())
	}
	cidr := cfg.ClusterPodCIDR("shoulders")
	second := 0
	if _, err := fmt.Sscanf(cidr, "10.%d.0.0/16", &second); err != nil {
		t.Fatalf("unexpected CIDR shape %q: %v", cidr, err)
	}
	if second < 200 || second > 255 {
		t.Fatalf("CIDR %q second octet %d outside the configured base 200", cidr, second)
	}
}

func TestMeshEncryptionIsOffByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.MeshEncryption(); got != "" {
		t.Fatalf("encryption = %q, want empty (off) by default", got)
	}
	cfg.Platform.Mesh.Encryption = " WireGuard "
	if got := cfg.MeshEncryption(); got != "wireguard" {
		t.Fatalf("encryption = %q, want normalised wireguard", got)
	}
}

func TestPodCIDRForIsStableAndValid(t *testing.T) {
	first := PodCIDRFor("shoulders", 16)
	second := PodCIDRFor("shoulders", 16)
	if first != second {
		t.Fatalf("allocation is not stable: %q vs %q", first, second)
	}
	if err := ValidatePodCIDR(first); err != nil {
		t.Fatalf("allocated CIDR %q is invalid: %v", first, err)
	}
}

// TestPodCIDRForIsARealNetworkAddress is the regression test for a bug found by
// running the allocator live: it produced "10.244.231.0/16", which is not a
// valid /16 network address and silently normalises to 10.244.0.0/16. Every
// cluster therefore received the SAME range and the collision persisted.
func TestPodCIDRForIsARealNetworkAddress(t *testing.T) {
	names := []string{"shoulders", "hub", "spoke-a", "spoke-b", "dev-hub", "edge-1", "a", "b"}
	for _, name := range names {
		for _, base := range []int{16, 100, 200} {
			cidr := PodCIDRFor(name, base)
			if err := ValidatePodCIDR(cidr); err != nil {
				t.Fatalf("%s/base=%d produced %q which fails validation: %v", name, base, cidr, err)
			}
			if !strings.HasSuffix(cidr, ".0.0/16") {
				t.Fatalf("%s/base=%d produced %q; a /16 must end .0.0/16", name, base, cidr)
			}
		}
	}
}

func TestPodCIDRSlots(t *testing.T) {
	if got := podCIDRSlots(16); got != 240 {
		t.Fatalf("base 16 should yield 240 slots, got %d", got)
	}
	if got := podCIDRSlots(255); got != 1 {
		t.Fatalf("base 255 should yield 1 slot, got %d", got)
	}
	if got := podCIDRSlots(300); got != 1 {
		t.Fatalf("out-of-range base should clamp to 1 slot, got %d", got)
	}
}

func TestPodCIDRForDiffersPerCluster(t *testing.T) {
	seen := map[string]string{}
	names := []string{"shoulders", "spoke-a", "spoke-b", "hub", "dev-hub", "edge-1"}
	for _, name := range names {
		cidr := PodCIDRFor(name, 16)
		if prev, clash := seen[cidr]; clash {
			// Not fatal — 256 slots — but it is exactly what
			// PodCIDRForNames exists to report for a known cluster set.
			t.Logf("note: %q and %q collide on %s", prev, name, cidr)
		}
		seen[cidr] = name
	}
	assigned, err := PodCIDRForNames(names, 16)
	if err != nil {
		t.Fatalf("expected a collision-free allocation for %d clusters: %v", len(names), err)
	}
	if len(assigned) != len(names) {
		t.Fatalf("assigned %d CIDRs for %d clusters", len(assigned), len(names))
	}
}

func TestPodCIDRForNamesReportsCollisions(t *testing.T) {
	// Brute-force a colliding pair from the 240 available slots, then assert
	// the detector reports it rather than silently handing out a shared range.
	// FNV-1a with 240 slots is deterministic, so this known pair collides on
	// every run rather than depending on a search that might change.
	pair := []string{"cluster-6", "cluster-20"}
	if PodCIDRFor(pair[0], 16) != PodCIDRFor(pair[1], 16) {
		t.Fatalf("expected %s and %s to collide on base 16", pair[0], pair[1])
	}
	_, err := PodCIDRForNames(pair, 16)
	if err == nil {
		t.Fatal("expected a collision error")
	}
	if !strings.Contains(err.Error(), "podCIDRBase") {
		t.Fatalf("error should suggest a remedy, got: %v", err)
	}
	if !strings.Contains(err.Error(), pair[0]) || !strings.Contains(err.Error(), pair[1]) {
		t.Fatalf("error should name both clusters, got: %v", err)
	}

	// Distinct clusters that do not collide allocate cleanly.
	assigned, err := PodCIDRForNames([]string{"hub", "spoke-a", "spoke-b"}, 16)
	if err != nil {
		t.Fatalf("unexpected collision: %v", err)
	}
	if len(assigned) != 3 {
		t.Fatalf("expected 3 allocations, got %d", len(assigned))
	}
}

func TestValidatePodCIDR(t *testing.T) {
	cases := []struct {
		cidr     string
		wantErr  bool
		contains string
	}{
		{cidr: "10.16.0.0/16"},
		{cidr: "10.16.7.0/24", wantErr: true, contains: "must be a /16"},
		{cidr: "not-a-cidr", wantErr: true, contains: "invalid pod CIDR"},
		{cidr: "8.8.8.0/16", wantErr: true, contains: "RFC1918"},
		{cidr: "10.244.231.0/16", wantErr: true, contains: "not a network address"},
	}
	for _, tc := range cases {
		err := ValidatePodCIDR(tc.cidr)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error", tc.cidr)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("%s: error %q should contain %q", tc.cidr, err, tc.contains)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.cidr, err)
		}
	}
}
