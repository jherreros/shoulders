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
	defer func() {
		if err := gz.Close(); err != nil {
			t.Errorf("close snapshot gzip: %v", err)
		}
	}()
	// os.Root keeps every archive entry inside dir even if a header contains an
	// absolute path or "..". The snapshot is generated locally, but the test is
	// still the place where a malformed artifact would be caught.
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open extraction root: %v", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("close extraction root: %v", err)
		}
	}()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		name := filepath.FromSlash(header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				t.Fatalf("mkdir %v: %v", filepath.Dir(name), err)
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read %s: %v", header.Name, err)
			}
			// Preserve the recorded mode: a mode that makes the file
			// unreadable would make kustomize skip it silently.
			if err := root.WriteFile(name, body, os.FileMode(header.Mode)); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			if err := root.Chmod(name, os.FileMode(header.Mode)); err != nil {
				t.Fatalf("chmod %s: %v", name, err)
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
