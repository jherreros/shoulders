package config

import (
	"fmt"
	"strconv"
	"strings"
)

func ApplyOverrides(cfg *Config, entries []string) error {
	if cfg == nil {
		return fmt.Errorf("config override target is nil")
	}
	for _, entry := range entries {
		key, value, err := parseOverride(entry)
		if err != nil {
			return err
		}

		switch key {
		case "current_workspace":
			cfg.CurrentWorkspace = value
		case "cluster.provider":
			cfg.Cluster.Provider = value
		case "cluster.name":
			cfg.Cluster.Name = value
		case "cluster.kubeconfig":
			cfg.Cluster.Kubeconfig = value
		case "cluster.context":
			cfg.Cluster.Context = value
		case "platform.profile":
			cfg.Platform.Profile = value
		case "platform.domain":
			cfg.Platform.Domain = value
		case "platform.cilium.enabled":
			enabled, err := parseBoolOverride(key, value)
			if err != nil {
				return err
			}
			cfg.Platform.Cilium.Enabled = &enabled
		case "platform.cilium.version":
			cfg.Platform.Cilium.Version = value
		case "platform.flux.gitRepository.url":
			cfg.Platform.Flux.GitRepository.URL = value
		case "platform.flux.gitRepository.branch":
			cfg.Platform.Flux.GitRepository.Branch = value
		case "platform.flux.pathPrefix":
			cfg.Platform.Flux.PathPrefix = value
		default:
			return unsupportedOverrideKeyError(key)
		}
	}
	return nil
}

func SupportedOverrideKeys() []string {
	return []string{
		"current_workspace",
		"cluster.provider",
		"cluster.name",
		"cluster.kubeconfig",
		"cluster.context",
		"platform.profile",
		"platform.domain",
		"platform.cilium.enabled",
		"platform.cilium.version",
		"platform.flux.gitRepository.url",
		"platform.flux.gitRepository.branch",
		"platform.flux.pathPrefix",
	}
}

func parseOverride(entry string) (string, string, error) {
	key, value, ok := strings.Cut(entry, "=")
	if !ok {
		return "", "", fmt.Errorf("invalid config override %q, expected key=value", entry)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("invalid config override %q, empty key", entry)
	}
	return key, strings.TrimSpace(value), nil
}

func parseBoolOverride(key, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value %q for config override %q", value, key)
	}
	return parsed, nil
}

func unsupportedOverrideKeyError(key string) error {
	return fmt.Errorf("unsupported config override %q (supported: %s)", key, strings.Join(SupportedOverrideKeys(), ", "))
}
