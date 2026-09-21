package airgap

import (
	"os"
	"strings"
	"testing"
)

func TestDigestOnlyRef(t *testing.T) {
	for input, want := range map[string]string{
		"quay.io/cilium/cilium:v1.19.6@sha256:abc123": "quay.io/cilium/cilium@sha256:abc123",
		"quay.io/cilium/cilium@sha256:abc123":         "quay.io/cilium/cilium@sha256:abc123",
		"nginx:1.29":                                  "nginx:1.29",
		"localhost:5000/repo:tag":                     "localhost:5000/repo:tag",
	} {
		if got := digestOnlyRef(input); got != want {
			t.Fatalf("digestOnlyRef(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSaveRef(t *testing.T) {
	if got := saveRef("quay.io/x:v1@sha256:abc"); got != "quay.io/x:v1" {
		t.Fatalf("unexpected saveRef: %q", got)
	}
	if got := saveRef("quay.io/x:v1"); got != "quay.io/x:v1" {
		t.Fatalf("unexpected saveRef: %q", got)
	}
}

func TestSanitizeImageNameUnique(t *testing.T) {
	seen := map[string]bool{}
	refs := []string{
		"quay.io/cilium/cilium:v1.19.6@sha256:aaa",
		"quay.io/cilium/cilium:v1.19.6@sha256:bbb",
		"docker.io/library/nginx:1.29",
	}
	for i, ref := range refs {
		name := sanitizeImageName(ref)
		if strings.ContainsAny(name, "/:@") {
			t.Fatalf("sanitized name %q contains unsafe characters", name)
		}
		_ = i
		seen[name] = true
	}
	if len(seen) != len(refs) {
		t.Fatalf("sanitized names collide: %v", seen)
	}
}

func TestBundleImageTars(t *testing.T) {
	dir := t.TempDir()
	imagesDir := dir + "/images"
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"00-quay-io-cilium-cilium-v1.19.6.tar", "01-ghcr-io-dexidp-dex-v2.44.0.tar"} {
		if err := os.WriteFile(imagesDir+"/"+name, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := BundleImageTars(dir, 2)
	if err != nil {
		t.Fatalf("BundleImageTars failed: %v", err)
	}
	if len(got) != 2 || !strings.HasSuffix(got[0], "00-quay-io-cilium-cilium-v1.19.6.tar") || !strings.HasSuffix(got[1], "01-ghcr-io-dexidp-dex-v2.44.0.tar") {
		t.Fatalf("unexpected tar order: %q", got)
	}
	if _, err := BundleImageTars(dir, 3); err == nil {
		t.Fatalf("expected count mismatch error")
	}
}
