package ocisource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotIsBuildableByKustomize checks that the artifact the CLI produces
// can be built by kustomize.
//
// This exists because of a live defect: flux's kustomize builder produced 12
// objects where `kubectl kustomize` produced 13, silently dropping the Cilium
// HelmRelease from the GitOps set. If the snapshot itself is the problem — a
// file mode, a path, an ordering — it has to be reproducible without a
// cluster, or chasing it costs a cluster per experiment.
func TestSnapshotIsBuildableByKustomize(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	snapshot, err := BuildSnapshot(repoRoot, testClock())
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	// Extract into a temp dir: this is what the source-controller does when it
	// materialises the artifact.
	dir := t.TempDir()
	gz, err := gzip.NewReader(bytes.NewReader(snapshot.Payload))
	if err != nil {
		t.Fatalf("gunzip snapshot: %v", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		target := filepath.Join(dir, filepath.Clean(header.Name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatalf("mkdir %v: %v", filepath.Dir(target), err)
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read %s: %v", header.Name, err)
			}
			// Preserve the recorded mode: a mode that makes the file
			// unreadable would make kustomize skip it silently.
			if err := os.WriteFile(target, body, os.FileMode(header.Mode)); err != nil {
				t.Fatalf("write %s: %v", target, err)
			}
			if err := os.Chmod(target, os.FileMode(header.Mode)); err != nil {
				t.Fatalf("chmod %s: %v", target, err)
			}
		}
	}

	// The Cilium HelmRelease must be present in the artifact at all.
	cilium := filepath.Join(dir, "2-addons", "manifests", "helm-releases", "cilium.yaml")
	info, err := os.Stat(cilium)
	if err != nil {
		t.Fatalf("cilium.yaml missing from the snapshot: %v", err)
	}
	if info.Mode().Perm()&0o400 == 0 {
		t.Errorf("cilium.yaml is not readable in the snapshot (mode %v); kustomize would skip it", info.Mode())
	}

	// And the profile path must build.
	buildKustomization(t, dir, "2-addons/profiles/small/helm-releases")
}
