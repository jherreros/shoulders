package cmd

import (
	"testing"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

func TestUpPhasesIncludesBundleImport(t *testing.T) {
	originalConfig := currentConfig
	originalBundle := upBundle
	defer func() {
		currentConfig = originalConfig
		upBundle = originalBundle
	}()

	currentConfig = config.DefaultConfig()
	upBundle = ""
	if got := len(upPhases()); got != 7 {
		t.Fatalf("expected 7 phases without bundle, got %d: %v", got, upPhases())
	}

	// The bundle install runs one more tracker step than the phase list
	// historically had, panicking PhaseTracker.Start at the very end of a
	// successful install. The phase list must cover it.
	upBundle = "shoulders-bundle.tar.gz"
	phases := upPhases()
	if len(phases) != 8 {
		t.Fatalf("expected 8 phases with bundle, got %d: %v", len(phases), phases)
	}
	if phases[1] != "Import airgap bundle" {
		t.Fatalf("expected bundle import as second phase, got %v", phases)
	}
}

func TestCurrentNamespaceOverride(t *testing.T) {
	originalOverride := namespaceOverride
	originalConfig := currentConfig
	defer func() {
		namespaceOverride = originalOverride
		currentConfig = originalConfig
	}()

	namespaceOverride = "override"
	currentConfig = &config.Config{CurrentWorkspace: "team-a"}

	ns, err := currentNamespace()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != "override" {
		t.Fatalf("expected override namespace, got %s", ns)
	}
}

func TestCurrentNamespaceFromConfig(t *testing.T) {
	originalOverride := namespaceOverride
	originalConfig := currentConfig
	defer func() {
		namespaceOverride = originalOverride
		currentConfig = originalConfig
	}()

	namespaceOverride = ""
	currentConfig = &config.Config{CurrentWorkspace: "team-b"}

	ns, err := currentNamespace()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != "team-b" {
		t.Fatalf("expected team-b namespace, got %s", ns)
	}
}

func TestCurrentNamespaceMissing(t *testing.T) {
	originalOverride := namespaceOverride
	originalConfig := currentConfig
	defer func() {
		namespaceOverride = originalOverride
		currentConfig = originalConfig
	}()

	namespaceOverride = ""
	currentConfig = nil

	_, err := currentNamespace()
	if err == nil {
		t.Fatalf("expected error when namespace missing")
	}
}

func TestGatewayChecksRequiredDefaultsToTrue(t *testing.T) {
	originalConfig := currentConfig
	defer func() {
		currentConfig = originalConfig
	}()

	currentConfig = nil
	if !gatewayChecksRequired() {
		t.Fatalf("expected gateway checks to be required by default")
	}
}

func TestGatewayChecksRequiredDisabledWhenCiliumDisabled(t *testing.T) {
	originalConfig := currentConfig
	defer func() {
		currentConfig = originalConfig
	}()

	enabled := false
	currentConfig = &config.Config{Platform: config.PlatformConfig{Cilium: config.CiliumConfig{Enabled: &enabled}}}
	if gatewayChecksRequired() {
		t.Fatalf("expected gateway checks to be skipped when cilium is disabled")
	}
}

func TestProfileWaitTargets(t *testing.T) {
	small := config.ProfileSpecFor(config.ProfileSmall)
	medium := config.ProfileSpecFor(config.ProfileMedium)

	if hasNamedResource(platformDeploymentsForProfile(small), "policy-reporter", "policy-reporter-ui") {
		t.Fatalf("small profile should not wait for policy reporter deployment")
	}
	if hasNamedResource(gatewayRoutesForProfile(small), "policy-reporter", "policy-reporter") {
		t.Fatalf("small profile should not wait for policy reporter route")
	}
	if !hasNamedResource(platformDeploymentsForProfile(medium), "policy-reporter", "policy-reporter-ui") {
		t.Fatalf("medium profile should wait for policy reporter deployment")
	}
	if !hasNamedResource(gatewayRoutesForProfile(medium), "policy-reporter", "policy-reporter") {
		t.Fatalf("medium profile should wait for policy reporter route")
	}
}

func hasNamedResource(resources []namedPlatformResource, namespace, name string) bool {
	for _, resource := range resources {
		if resource.ns == namespace && resource.name == name {
			return true
		}
	}
	return false
}
