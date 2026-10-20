package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSopsKeyFileDefaultsOutsideTheRepo(t *testing.T) {
	path, explicit, err := SopsConfig{}.KeyFile()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if explicit {
		t.Fatal("the default key is not explicit: a missing one must not fail an install")
	}
	// The sops CLI's own key path: encrypting and decrypting then needs no
	// environment variables.
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("config dir: %v", err)
	}
	if path != filepath.Join(dir, "sops", "age", "keys.txt") {
		t.Fatalf("default path = %q", path)
	}
}

func TestSopsKeyFileExpandsHome(t *testing.T) {
	path, explicit, err := SopsConfig{AgeKeyFile: "~/keys/topology.agekey"}.KeyFile()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !explicit {
		t.Fatal("a configured key is explicit")
	}
	if strings.HasPrefix(path, "~") {
		t.Fatalf("home prefix was not expanded: %q", path)
	}
	home, _ := os.UserHomeDir()
	if path != filepath.Join(home, "keys", "topology.agekey") {
		t.Fatalf("path = %q", path)
	}
}
