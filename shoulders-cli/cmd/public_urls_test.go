package cmd

import (
	"testing"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

func TestSelectedPublicURLsUseResolvedClusterDomain(t *testing.T) {
	cfg := &config.Config{
		Clusters: []config.ClusterEntry{
			{
				Name:     "hub",
				Role:     "hub",
				Provider: "existing",
				Context:  "hub-context",
				Domain:   "hub.example.test",
			},
			{
				Name:     "spoke-a",
				Role:     "spoke",
				Provider: "existing",
				Context:  "spoke-context",
			},
		},
	}

	topology, err := cfg.Topology()
	if err != nil {
		t.Fatalf("resolve topology: %v", err)
	}
	hub, ok := topology.Get("hub")
	if !ok {
		t.Fatal("hub not found in topology")
	}

	// The shared platform block has no domain. The hub entry does; public
	// URLs must follow the selected cluster rather than currentConfig.
	if got, want := selectedHeadlampURL(hub), "http://headlamp.hub.example.test"; got != want {
		t.Fatalf("headlamp URL = %q, want %q", got, want)
	}
	if got, want := selectedGrafanaURL(hub), "http://grafana.hub.example.test"; got != want {
		t.Fatalf("grafana URL = %q, want %q", got, want)
	}
	if got, want := selectedReporterURL(hub), "http://reporter.hub.example.test"; got != want {
		t.Fatalf("reporter URL = %q, want %q", got, want)
	}
}
