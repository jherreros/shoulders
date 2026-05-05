package cmd

import "testing"

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
