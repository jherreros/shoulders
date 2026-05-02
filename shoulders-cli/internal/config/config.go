package config

import (
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

func Load(pathOverride ...string) (*Config, error) {
	return LoadWithOverrides(nil, pathOverride...)
}

func LoadWithOverrides(overrides []string, pathOverride ...string) (*Config, error) {
	configPath, err := Path(pathOverride...)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			data = nil
		} else {
			return nil, err
		}
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	if err := ApplyOverrides(cfg, overrides); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(cfg *Config, pathOverride ...string) error {
	configPath, err := Path(pathOverride...)
	if err != nil {
		return err
	}
	clone := *cfg
	clone.ApplyDefaults()
	if err := clone.Validate(); err != nil {
		return err
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content, err := yaml.Marshal(&clone)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, content, 0o644)
}

func Path(pathOverride ...string) (string, error) {
	if len(pathOverride) > 0 && pathOverride[0] != "" {
		return expandPath(pathOverride[0])
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".shoulders", "config.yaml"), nil
}

func expandPath(path string) (string, error) {
	if path == "" {
		return Path()
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path), nil
}
