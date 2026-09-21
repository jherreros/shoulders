package ocisource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}

func TestBuildSnapshotPackagesOnlyAddons(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root+"/2-addons/manifests/crossplane/xrd.yaml", "apiVersion: v1\n")
	writeFile(t, root+"/2-addons/flux/kustomization.yaml", "resources: []\n")
	writeFile(t, root+"/README.md", "hello\n")
	writeFile(t, root+"/shoulders-cli/main.go", "package main\n")
	writeFile(t, root+"/.git/HEAD", "ref: refs/heads/main\n")

	snapshot, err := BuildSnapshot(root, time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("build snapshot failed: %v", err)
	}
	if snapshot.Files != 2 {
		t.Fatalf("expected 2 files in snapshot, got %d", snapshot.Files)
	}
	names := tarNames(t, snapshot.Payload)
	for _, want := range []string{"2-addons/manifests/crossplane/xrd.yaml", "2-addons/flux/kustomization.yaml"} {
		if !names[want] {
			t.Fatalf("expected snapshot to contain %q, got %v", want, keys(names))
		}
	}
	for name := range names {
		if !strings.HasPrefix(name, "2-addons/") {
			t.Fatalf("snapshot must only contain 2-addons entries, found %q", name)
		}
	}
	// Outside a git checkout the tag still resolves.
	if snapshot.SHA != "nogit" {
		t.Fatalf("expected nogit sha outside a repo, got %q", snapshot.SHA)
	}
	if !strings.HasSuffix(snapshot.Tag, "-dirty") {
		t.Fatalf("expected dirty suffix for untracked tree, got %q", snapshot.Tag)
	}
}

func TestBuildSnapshotRequiresAddonsDir(t *testing.T) {
	if _, err := BuildSnapshot(t.TempDir(), time.Now()); err == nil {
		t.Fatalf("expected error when 2-addons is missing")
	}
}

func TestBuildSnapshotWithOverlay(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root+"/2-addons/manifests/helm-releases/app.yaml", "original: true\n")
	writeFile(t, root+"/2-addons/manifests/other.yaml", "other: true\n")

	extra := map[string][]byte{
		"2-addons/manifests/helm-releases/app.yaml": []byte("rewritten: true\n"),
		"2-addons/manifests/generated.yaml":         []byte("generated: true\n"),
	}
	snapshot, err := BuildSnapshotWithOverlay(root, time.Now(), extra)
	if err != nil {
		t.Fatalf("build snapshot with overlay failed: %v", err)
	}
	if snapshot.Files != 3 {
		t.Fatalf("expected 3 files in snapshot, got %d", snapshot.Files)
	}
	payloads := tarPayloads(t, snapshot.Payload)
	if string(payloads["2-addons/manifests/helm-releases/app.yaml"]) != "rewritten: true\n" {
		t.Fatalf("expected overlaid content, got %q", payloads["2-addons/manifests/helm-releases/app.yaml"])
	}
	if string(payloads["2-addons/manifests/generated.yaml"]) != "generated: true\n" {
		t.Fatalf("expected generated file, got %q", payloads["2-addons/manifests/generated.yaml"])
	}
	if string(payloads["2-addons/manifests/other.yaml"]) != "other: true\n" {
		t.Fatalf("expected untouched file, got %q", payloads["2-addons/manifests/other.yaml"])
	}
}

func tarPayloads(t *testing.T, payload []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("gunzip failed: %v", err)
	}
	defer func() {
		_ = gz.Close()
	}()
	reader := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("read tar failed: %v", err)
		}
		if !header.FileInfo().Mode().IsRegular() {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read entry failed: %v", err)
		}
		out[header.Name] = content
	}
}

func TestNewTagFormat(t *testing.T) {
	valid := regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
	for _, tag := range []string{
		NewTag("abc123", false, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		NewTag("nogit", true, time.Now()),
		NewTag("feature/branch!", true, time.Now()),
	} {
		if !valid.MatchString(tag) {
			t.Fatalf("tag %q is not a valid OCI tag", tag)
		}
	}
	if got := NewTag("abc123", false, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got != "local-abc123-20260102030405" {
		t.Fatalf("unexpected clean tag: %q", got)
	}
	if got := NewTag("abc123", true, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got != "local-abc123-20260102030405-dirty" {
		t.Fatalf("unexpected dirty tag: %q", got)
	}
}

func TestSplitOCIURL(t *testing.T) {
	addr, path, err := SplitOCIURL("oci://registry.example.com:5000/shoulders/addons")
	if err != nil {
		t.Fatalf("split failed: %v", err)
	}
	if addr != "registry.example.com:5000" || path != "shoulders/addons" {
		t.Fatalf("unexpected split: addr=%q path=%q", addr, path)
	}
	for _, bad := range []string{
		"https://example.com/repo",
		"oci://only-host",
		"oci://",
		"",
	} {
		if _, _, err := SplitOCIURL(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestRegistryManifestIsValid(t *testing.T) {
	manifest := RegistryManifest()
	docs := strings.Split(strings.TrimSpace(string(manifest)), "\n---\n")
	if len(docs) != 3 {
		t.Fatalf("expected namespace + deployment + service, got %d documents", len(docs))
	}
	kinds := map[string]bool{}
	for _, doc := range docs {
		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			t.Fatalf("registry manifest document should be valid YAML: %v\n%s", err, doc)
		}
		kind, _ := parsed["kind"].(string)
		kinds[kind] = true
	}
	for _, want := range []string{"Namespace", "Deployment", "Service"} {
		if !kinds[want] {
			t.Fatalf("expected registry manifest to contain %s, got %v", want, kinds)
		}
	}
	for _, want := range []string{
		"containerPort: 5000",
		"image: " + RegistryImage,
		"claimName: " + RegistryDataPVC,
		"mountPath: /var/lib/registry",
	} {
		if !strings.Contains(string(manifest), want) {
			t.Fatalf("expected registry manifest to contain %q\n%s", want, manifest)
		}
	}

	pvc := string(RegistryDataPVCManifest())
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(pvc), &parsed); err != nil {
		t.Fatalf("registry PVC manifest should be valid YAML: %v", err)
	}
	if parsed["kind"] != "PersistentVolumeClaim" {
		t.Fatalf("expected PersistentVolumeClaim, got %v", parsed["kind"])
	}
	for _, want := range []string{"name: " + RegistryDataPVC, "storage: " + RegistryDataSize} {
		if !strings.Contains(pvc, want) {
			t.Fatalf("expected registry PVC manifest to contain %q\n%s", want, pvc)
		}
	}
}

func tarNames(t *testing.T, payload []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("gunzip failed: %v", err)
	}
	defer func() {
		_ = gz.Close()
	}()
	reader := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatalf("read tar failed: %v", err)
		}
		if header.FileInfo().Mode().IsRegular() {
			names[header.Name] = true
		}
	}
}

func keys(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	return out
}
