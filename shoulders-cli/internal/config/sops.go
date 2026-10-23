package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SopsConfig configures secret decryption.
//
// Flux decrypts files in the cluster; the CLI's only job is to put the age
// identity where flux can read it. The identity is never written into git and
// never into the topology's config file.
type SopsConfig struct {
	// AgeKeyFile is the age identity flux decrypts with. Empty means the
	// default: the sops CLI's own key file, so `sops -e`/`-d` need no
	// environment variables.
	AgeKeyFile string `yaml:"ageKeyFile,omitempty" json:"ageKeyFile,omitempty"`
}

// DefaultAgeKeyFile is where `shoulders secrets init` writes the identity.
//
// It is deliberately the path the sops CLI itself uses
// ($XDG_CONFIG_HOME/sops/age/keys.txt, or the platform's config directory), so
// `sops -e` and `sops -d` work without setting SOPS_AGE_KEY_FILE. The private
// key still lives outside the repository.
func DefaultAgeKeyFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate the config directory for the age key: %w", err)
	}
	return filepath.Join(dir, "sops", "age", "keys.txt"), nil
}

// KeyFile resolves the configured path, expanding a leading ~.
//
// It also reports whether the path was configured explicitly. A missing
// explicit key is an error — the operator asked for a file, and installing
// without decryption would be a silent downgrade. A missing default key simply
// means this topology does not use SOPS yet.
func (s SopsConfig) KeyFile() (path string, explicit bool, err error) {
	if strings.TrimSpace(s.AgeKeyFile) == "" {
		path, err := DefaultAgeKeyFile()
		return path, false, err
	}
	path = s.AgeKeyFile
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", true, fmt.Errorf("expand %q: %w", s.AgeKeyFile, err)
		}
		path = filepath.Join(home, path[2:])
	}
	return path, true, nil
}
