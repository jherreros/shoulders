package bootstrap

import (
	"strings"
	"testing"
)

func TestPortConflictErrorNamesCluster(t *testing.T) {
	err := portConflictError(80, "vcluster.cp.shoulders-airgap")
	if err == nil {
		t.Fatal("expected error for held port")
	}
	msg := err.Error()
	if !strings.Contains(msg, "shoulders-airgap") {
		t.Fatalf("expected cluster name in error, got %q", msg)
	}
	if !strings.Contains(msg, "shoulders down --name shoulders-airgap") {
		t.Fatalf("expected down remedy in error, got %q", msg)
	}
}

func TestPortConflictErrorGenericContainer(t *testing.T) {
	err := portConflictError(443, "my-nginx")
	if err == nil {
		t.Fatal("expected error for held port")
	}
	msg := err.Error()
	if !strings.Contains(msg, "my-nginx") {
		t.Fatalf("expected container name in error, got %q", msg)
	}
	if !strings.Contains(msg, "shoulders cluster list") {
		t.Fatalf("expected cluster list hint in error, got %q", msg)
	}
}

func TestIsOwnControlPlane(t *testing.T) {
	if !isOwnControlPlane("vcluster.cp.shoulders", "shoulders") {
		t.Fatal("expected own control-plane to match")
	}
	for _, owner := range []string{"vcluster.cp.shoulders-airgap", "port80squat", "", "vcluster.cp.shoulders-extra"} {
		if isOwnControlPlane(owner, "shoulders") {
			t.Fatalf("expected %q to not match cluster shoulders", owner)
		}
	}
}

func TestProfileDiskMinimumOrdering(t *testing.T) {
	small, medium, large := ProfileDiskMinimum("small"), ProfileDiskMinimum("medium"), ProfileDiskMinimum("large")
	if small >= medium || medium >= large {
		t.Fatalf("expected small < medium < large minimums, got %d %d %d", small, medium, large)
	}
	if small != 25<<30 || medium != 40<<30 || large != 60<<30 {
		t.Fatalf("minimums diverged from documented values, got %d %d %d", small, medium, large)
	}
	if ProfileDiskMinimum("unknown") != small {
		t.Fatal("expected unknown profile to fall back to small minimum")
	}
}

func TestFreeBytesTempDir(t *testing.T) {
	free, err := freeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("freeBytes: %v", err)
	}
	if free < 1<<20 {
		t.Fatalf("expected at least 1MiB free in temp dir, got %d", free)
	}
}

func TestCheckDiskHeadroom(t *testing.T) {
	if err := CheckDiskHeadroom(1); err != nil {
		t.Fatalf("expected tiny requirement to pass, got %v", err)
	}
	if err := CheckDiskHeadroom(1 << 60); err == nil {
		t.Fatal("expected impossible requirement to fail")
	} else if !strings.Contains(err.Error(), "prune") {
		t.Fatalf("expected prune remedy in error, got %q", err.Error())
	}
}

func TestDockerResourceWarning(t *testing.T) {
	if got := dockerResourceWarning(11, 16<<30); got != "" {
		t.Fatalf("expected no warning for healthy allocation, got %q", got)
	}
	got := dockerResourceWarning(4, 4<<30)
	if !strings.Contains(got, "4 Docker CPUs") || !strings.Contains(got, "4.0 GiB") {
		t.Fatalf("expected CPU and memory in warning, got %q", got)
	}
	if got := dockerResourceWarning(0, 0); got != "" {
		t.Fatalf("expected no warning when info unavailable, got %q", got)
	}
}
