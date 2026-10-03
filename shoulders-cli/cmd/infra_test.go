package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestInfraAddCommandsExposeDryRun(t *testing.T) {
	for _, cmd := range []*cobra.Command{infraAddDbCmd, infraAddBucketCmd, infraAddStreamCmd} {
		if flag := cmd.Flags().Lookup("dry-run"); flag == nil {
			t.Fatalf("expected %s to expose --dry-run", cmd.Name())
		}
	}
}

func TestInfraAddStreamDoesNotShadowGlobalConfigFlag(t *testing.T) {
	if flag := infraAddStreamCmd.LocalFlags().Lookup("config"); flag != nil {
		t.Fatalf("add-stream must not define local --config because it shadows the global config-file flag")
	}
	if flag := infraAddStreamCmd.Flags().Lookup("topic-config"); flag == nil {
		t.Fatalf("expected add-stream to expose --topic-config")
	}
}

func TestParseConfig(t *testing.T) {
	config, err := parseConfig([]string{"cleanup.policy=compact", "retention.ms=60000"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config["cleanup.policy"] != "compact" {
		t.Fatalf("expected cleanup.policy=compact")
	}
}

func TestParseConfigInvalid(t *testing.T) {
	_, err := parseConfig([]string{"invalid"})
	if err == nil {
		t.Fatalf("expected error for invalid entry")
	}
}

func TestParseList(t *testing.T) {
	items := parseList("app, ledger\naccounts")
	if len(items) != 3 || items[0] != "app" || items[2] != "accounts" {
		t.Fatalf("unexpected list parse result: %#v", items)
	}
}

func TestBuildDBResources(t *testing.T) {
	oldTier, oldCPU, oldMem, oldCPULim, oldMemLim := dbTier, dbCPURequest, dbMemoryRequest, dbCPULimit, dbMemoryLimit
	defer func() {
		dbTier, dbCPURequest, dbMemoryRequest, dbCPULimit, dbMemoryLimit = oldTier, oldCPU, oldMem, oldCPULim, oldMemLim
	}()

	dbTier, dbCPURequest, dbMemoryRequest, dbCPULimit, dbMemoryLimit = "dev", "", "", "", ""
	if buildDBResources() != nil {
		t.Fatal("expected nil resources for dev tier without flags (BestEffort default)")
	}

	dbTier = "prod"
	resources := buildDBResources()
	requests, ok := resources["requests"].(map[string]interface{})
	if !ok || requests["cpu"] != "250m" || requests["memory"] != "512Mi" {
		t.Fatalf("expected prod default requests, got %#v", resources)
	}

	dbCPURequest, dbMemoryRequest, dbCPULimit, dbMemoryLimit = "500m", "1Gi", "2000m", "2Gi"
	resources = buildDBResources()
	requests, _ = resources["requests"].(map[string]interface{})
	limits, _ := resources["limits"].(map[string]interface{})
	if requests["cpu"] != "500m" || requests["memory"] != "1Gi" || limits["cpu"] != "2000m" || limits["memory"] != "2Gi" {
		t.Fatalf("expected explicit flags to win, got %#v", resources)
	}
}
