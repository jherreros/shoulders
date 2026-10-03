package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveKubeconfigPath returns the kubeconfig file `up` will overwrite:
// the explicit flag value, else the first entry of $KUBECONFIG, else the
// default ~/.kube/config. Empty means no file path could be determined.
func ResolveKubeconfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		if first := filepath.SplitList(env)[0]; first != "" {
			return first
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

// BackupKubeconfig snapshots the kubeconfig file before cluster creation
// rewrites it. It returns a restore function that writes the snapshot back
// (or nil when there is nothing to back up: unknown path or missing file).
// Callers should invoke restore when creation fails: a failed vind create can
// otherwise leave the file nulled, producing confusing downstream errors
// (e.g. "Install Cilium CNI: no configuration has been provided").
func BackupKubeconfig(explicit string) (restore func() error, err error) {
	path := ResolveKubeconfigPath(explicit)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("back up kubeconfig: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("back up kubeconfig: %w", err)
	}
	return func() error {
		if wErr := os.WriteFile(path, data, info.Mode()); wErr != nil {
			return fmt.Errorf("restore kubeconfig backup: %w", wErr)
		}
		return nil
	}, nil
}
