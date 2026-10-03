package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveKubeconfigPathExplicit(t *testing.T) {
	if got := ResolveKubeconfigPath("/tmp/custom.yaml"); got != "/tmp/custom.yaml" {
		t.Fatalf("expected explicit path passthrough, got %q", got)
	}
}

func TestResolveKubeconfigPathFromEnv(t *testing.T) {
	t.Setenv("KUBECONFIG", "first.yaml"+string(filepath.ListSeparator)+"second.yaml")
	if got := ResolveKubeconfigPath(""); got != "first.yaml" {
		t.Fatalf("expected first KUBECONFIG entry, got %q", got)
	}
}

func TestIsStaleCreatedContainer(t *testing.T) {
	if !isStaleCreatedContainer("created") {
		t.Fatal("expected \"created\" to be stale")
	}
	for _, status := range []string{"running", "exited", "paused", "restarting", "", "removing"} {
		if isStaleCreatedContainer(status) {
			t.Fatalf("expected %q to be left alone", status)
		}
	}
}

func TestBackupKubeconfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("clusters:\n- name: keepme\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	restore, err := BackupKubeconfig(path)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if restore == nil {
		t.Fatal("expected non-nil restore for existing file")
	}

	// Simulate the failed-run wipe (nulled kubeconfig).
	if err := os.WriteFile(path, []byte("clusters: null\n"), 0o600); err != nil {
		t.Fatalf("overwrite config: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected restore of %q, got %q", original, got)
	}
}

func TestBackupKubeconfigMissingFile(t *testing.T) {
	restore, err := BackupKubeconfig(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if restore != nil {
		t.Fatal("expected nil restore for missing file")
	}
}
