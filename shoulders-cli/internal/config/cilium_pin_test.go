package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ciliumPinPattern extracts the chart version from the GitOps Cilium HelmRelease.
var ciliumPinPattern = regexp.MustCompile(`(?m)^\s{6}version:\s*"?([^"\s]+)"?\s*$`)

// gitOpsCiliumManifest is the Flux-managed Cilium release. It is the source of
// truth for the Cilium version: Renovate keeps it current.
//
// Relative to this package's directory.
const gitOpsCiliumManifest = "../../../2-addons/manifests/helm-releases/cilium.yaml"

// TestDefaultCiliumVersionMatchesGitOpsPin keeps the CLI's bootstrap version and
// the GitOps pin in step.
//
// Cilium is installed by the CLI first (Flux needs a CNI) and then adopted by
// Flux from the HelmRelease in 2-addons. If the two versions differ, every
// install is silently upgraded one sync interval later. That is not a harmless
// drift: it is how 1.19.2 in the CLI coexisted with 1.20.2 in git for long
// enough for nobody to notice which one was actually running.
//
// The test skips when the manifest is not reachable, so the module still builds
// and tests when consumed outside the repository.
func TestDefaultCiliumVersionMatchesGitOpsPin(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(gitOpsCiliumManifest))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("GitOps manifest not present at %s; skipping drift guard", gitOpsCiliumManifest)
		}
		t.Fatalf("read %s: %v", gitOpsCiliumManifest, err)
	}

	match := ciliumPinPattern.FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatalf("could not find a chart version in %s; if the manifest's shape changed, update this guard",
			gitOpsCiliumManifest)
	}
	gitVersion := strings.TrimSpace(match[1])

	if gitVersion != DefaultCiliumVersion {
		t.Fatalf("Cilium version drift: CLI bootstraps %q but git pins %q in %s.\n"+
			"The CLI installs Cilium first and Flux adopts it afterwards, so a mismatch means every\n"+
			"install is silently upgraded within one sync interval. Renovate owns the git copy; move\n"+
			"DefaultCiliumVersion with it (or update the manifest by hand).",
			DefaultCiliumVersion, gitVersion, gitOpsCiliumManifest)
	}
}
