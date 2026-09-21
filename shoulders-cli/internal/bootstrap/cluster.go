package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	loftlog "github.com/loft-sh/log"
	vcli "github.com/loft-sh/vcluster/pkg/cli"
	vclusterconfig "github.com/loft-sh/vcluster/pkg/cli/config"
	"github.com/loft-sh/vcluster/pkg/cli/flags"
	"github.com/sirupsen/logrus"
)

const (
	DefaultClusterName = "shoulders"

	// VindVersion is the vCluster release used by shoulders.
	VindVersion = "0.33.1"

	// ContextPrefix is prepended to the cluster name in kubeconfig contexts.
	ContextPrefix = "vcluster-docker_"

	// controlPlanePrefix is the Docker container name prefix for vind
	// control-plane containers (e.g. "vcluster.cp.shoulders").
	controlPlanePrefix = "vcluster.cp."

	clusterAuthConfigName = "authentication-config.yaml"
)

// EnsureVindCluster creates a vind (vCluster-in-Docker) cluster if it does
// not already exist. vindConfig is an optional base vCluster values YAML and
// authConfig is an optional API server auth config that is mounted into the
// control-plane container before bootstrap.
func EnsureVindCluster(ctx context.Context, name string, vindConfig, authConfig []byte, dexHost string) (err error) {
	exists, err := containerExists(ctx, controlPlanePrefix+name)
	if err != nil {
		return fmt.Errorf("check if cluster already exists: %w", err)
	}
	if exists {
		return nil
	}

	// The vCluster OCI library reads Docker's credential store when pulling
	// images from ghcr.io. A stale empty auth entry (e.g. from a previous
	// "docker login ghcr.io" that was never completed) causes the library to
	// send empty Basic auth, which GHCR rejects with 403 instead of allowing
	// an anonymous pull. Clear such entries before proceeding.
	ensureGHCRAccess()
	restoreDockerConfig, err := useAnonymousDockerConfigIfNeeded()
	if err != nil {
		return err
	}
	defer restoreDockerConfig()

	configPath, err := vclusterconfig.DefaultFilePath()
	if err != nil {
		return fmt.Errorf("determine vcluster config path: %w", err)
	}

	globalFlags := &flags.GlobalFlags{Config: configPath}
	logger := loftlog.NewStreamLogger(os.Stdout, os.Stderr, logrus.InfoLevel)

	options := &vcli.CreateOptions{
		ChartVersion:  VindVersion,
		Connect:       true,
		UpdateCurrent: true,
	}

	if len(vindConfig) > 0 || len(authConfig) > 0 {
		tmpDir, tErr := os.MkdirTemp("", "shoulders-vind-*")
		if tErr != nil {
			return fmt.Errorf("create temp dir: %w", tErr)
		}
		defer func() {
			if removeErr := os.RemoveAll(tmpDir); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("remove temp dir %q: %w", tmpDir, removeErr))
			}
		}()

		valuesPaths := make([]string, 0, 2)
		if len(vindConfig) > 0 {
			valuesPath := filepath.Join(tmpDir, "values.yaml")
			if wErr := os.WriteFile(valuesPath, vindConfig, 0o644); wErr != nil {
				return fmt.Errorf("write vind values: %w", wErr)
			}
			valuesPaths = append(valuesPaths, valuesPath)
		}

		if len(authConfig) > 0 {
			authConfigHostPath, pErr := writeClusterAuthConfig(configPath, name, authConfig)
			if pErr != nil {
				return pErr
			}

			overlayPath := filepath.Join(tmpDir, "shoulders-values.yaml")
			overlay := renderShouldersVindOverlay(authConfigHostPath, dexHost)
			if wErr := os.WriteFile(overlayPath, []byte(overlay), 0o644); wErr != nil {
				return fmt.Errorf("write shoulders vind values: %w", wErr)
			}
			valuesPaths = append(valuesPaths, overlayPath)
		}

		options.Values = valuesPaths
	}

	return vcli.CreateDocker(ctx, options, globalFlags, name, logger)
}

// EnsureExistingCluster validates connectivity to an already running cluster.
func EnsureExistingCluster(ctx context.Context, kubeconfigPath string) error {
	return kube.WaitForAPIServer(ctx, kubeconfigPath)
}

func writeClusterAuthConfig(configPath, clusterName string, authConfig []byte) (string, error) {
	authConfigHostPath := clusterAuthConfigHostPath(configPath, clusterName)
	if err := os.MkdirAll(filepath.Dir(authConfigHostPath), 0o755); err != nil {
		return "", fmt.Errorf("create auth config dir: %w", err)
	}
	if err := os.WriteFile(authConfigHostPath, authConfig, 0o644); err != nil {
		return "", fmt.Errorf("write auth config: %w", err)
	}

	return authConfigHostPath, nil
}

func clusterAuthConfigHostPath(configPath, clusterName string) string {
	return filepath.Join(filepath.Dir(configPath), "docker", "vclusters", clusterName, clusterAuthConfigName)
}

func renderShouldersVindOverlay(authConfigHostPath, dexHost string) string {
	hosts := append(strings.Fields(dexInternalHosts), dexHost)

	var b strings.Builder
	b.WriteString("controlPlane:\n")
	b.WriteString("  distro:\n")
	b.WriteString("    k8s:\n")
	b.WriteString("      apiServer:\n")
	b.WriteString("        extraArgs:\n")
	fmt.Fprintf(&b, "          - %q\n", "--authentication-config="+authConfigPath)
	b.WriteString("experimental:\n")
	b.WriteString("  docker:\n")
	b.WriteString("    args:\n")
	for _, host := range hosts {
		fmt.Fprintf(&b, "      - %q\n", "--add-host="+host+":"+defaultDexServiceIP)
	}
	b.WriteString("    volumes:\n")
	fmt.Fprintf(&b, "      - %q\n", authConfigHostPath+":"+authConfigPath+":ro")

	return b.String()
}

// DeleteVindCluster removes a vind cluster and its associated resources.
func DeleteVindCluster(ctx context.Context, name string) error {
	configPath, err := vclusterconfig.DefaultFilePath()
	if err != nil {
		return fmt.Errorf("determine vcluster config path: %w", err)
	}

	globalFlags := &flags.GlobalFlags{Config: configPath}
	logger := loftlog.NewStreamLogger(os.Stdout, os.Stderr, logrus.InfoLevel)

	options := &vcli.DeleteOptions{
		DeleteContext:  true,
		IgnoreNotFound: true,
	}

	// Best-effort vCluster deletion; ignore errors since we force-remove below.
	_ = vcli.DeleteDocker(ctx, nil, options, globalFlags, name, logger)

	// DeleteDocker may report success without actually removing the Docker
	// containers and volumes. Force-remove them to ensure a clean slate.
	containers, err := vindContainerNames(ctx, name)
	if err != nil {
		return fmt.Errorf("list cluster containers: %w", err)
	}
	for _, c := range containers {
		_ = removeContainer(ctx, c)
		for _, suffix := range []string{".bin", ".cni-bin", ".etc", ".var"} {
			_ = removeVolume(ctx, c+suffix)
		}
	}
	_ = os.Remove(clusterAuthConfigHostPath(globalFlags.Config, name))
	return nil
}

// StopVindCluster stops a vind cluster by stopping its Docker containers
// (control-plane and worker nodes) without deleting them.
func StopVindCluster(ctx context.Context, name string) error {
	containers, err := vindContainerNames(ctx, name)
	if err != nil {
		return fmt.Errorf("list cluster containers: %w", err)
	}
	for _, c := range containers {
		if err := stopContainer(ctx, c); err != nil {
			return fmt.Errorf("stop cluster %q: %w", name, err)
		}
	}
	return nil
}

// StartVindCluster starts a previously stopped vind cluster by starting its
// Docker containers (control-plane and worker nodes).
func StartVindCluster(ctx context.Context, name string) error {
	containers, err := vindContainerNames(ctx, name)
	if err != nil {
		return fmt.Errorf("list cluster containers: %w", err)
	}
	for _, c := range containers {
		if err := startContainer(ctx, c); err != nil {
			return fmt.Errorf("start cluster %q: %w", name, err)
		}
	}
	return nil
}

// vindContainerNames returns the Docker container names for a vind cluster.
func vindContainerNames(ctx context.Context, name string) ([]string, error) {
	names := []string{controlPlanePrefix + name}
	workers, err := listContainerNames(ctx, "vcluster.node."+name+".")
	if err != nil {
		return nil, fmt.Errorf("list worker containers: %w", err)
	}
	sort.Strings(workers)
	return append(names, workers...), nil
}

// VindContainers returns the Docker container names for a vind cluster
// (control plane first, then sorted workers). Exported for airgap image
// imports, which address the node containers directly.
func VindContainers(ctx context.Context, name string) ([]string, error) {
	return vindContainerNames(ctx, name)
}

// ListClusters returns the names of all vind clusters by inspecting Docker
// containers whose names start with the vind control-plane prefix.
func ListClusters() ([]string, error) {
	names, err := listContainerNames(context.Background(), controlPlanePrefix)
	if err != nil {
		return nil, fmt.Errorf("list vind clusters: %w", err)
	}

	var clusters []string
	for _, name := range names {
		if strings.HasPrefix(name, controlPlanePrefix) {
			clusters = append(clusters, strings.TrimPrefix(name, controlPlanePrefix))
		}
	}
	return clusters, nil
}

// ensureGHCRAccess removes stale empty ghcr.io auth entries from the Docker
// config so that subsequent OCI pulls can proceed anonymously. When Docker's
// config contains an empty auth entry for ghcr.io together with a credential
// store (e.g. "desktop"), the loft-sh/image library sends empty Basic
// credentials, which GHCR rejects with 403. Removing the empty entry
// allows the library to fall back to an unauthenticated token request.
func ensureGHCRAccess() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	configPath := filepath.Join(home, ".docker", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}

	authsRaw, ok := cfg["auths"]
	if !ok {
		return
	}

	var auths map[string]json.RawMessage
	if err := json.Unmarshal(authsRaw, &auths); err != nil {
		return
	}

	ghcrEntry, ok := auths["ghcr.io"]
	if !ok {
		return
	}

	// Only remove the entry if it's empty (i.e. "{}"), which indicates a
	// stale login with no actual credentials stored inline.
	var entryFields map[string]interface{}
	if err := json.Unmarshal(ghcrEntry, &entryFields); err != nil {
		return
	}
	if len(entryFields) > 0 {
		return // has real credentials, leave it alone
	}

	delete(auths, "ghcr.io")

	updated, err := json.Marshal(auths)
	if err != nil {
		return
	}
	cfg["auths"] = updated

	out, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return
	}

	// Preserve original file permissions.
	info, err := os.Stat(configPath)
	if err != nil {
		return
	}
	_ = os.WriteFile(configPath, append(out, '\n'), info.Mode())
}

func useAnonymousDockerConfigIfNeeded() (func(), error) {
	if os.Getenv("DOCKER_CONFIG") != "" {
		return func() {}, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return func() {}, nil
	}

	configPath := filepath.Join(home, ".docker", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return func() {}, nil
	}

	var cfg struct {
		Auths      map[string]json.RawMessage `json:"auths"`
		CredsStore string                     `json:"credsStore"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return func() {}, nil
	}

	if cfg.CredsStore != "desktop" {
		return func() {}, nil
	}

	tmpDir, err := os.MkdirTemp("", "shoulders-docker-config-*")
	if err != nil {
		return nil, fmt.Errorf("create temp docker config dir: %w", err)
	}

	clone := map[string]json.RawMessage{}
	auths, err := json.Marshal(cfg.Auths)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("marshal docker auths: %w", err)
	}
	clone["auths"] = auths

	out, err := json.MarshalIndent(clone, "", "\t")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("marshal temp docker config: %w", err)
	}

	tmpConfigPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(tmpConfigPath, append(out, '\n'), 0o600); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("write temp docker config: %w", err)
	}

	if err := os.Setenv("DOCKER_CONFIG", tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("set DOCKER_CONFIG: %w", err)
	}

	return func() {
		_ = os.Unsetenv("DOCKER_CONFIG")
		_ = os.RemoveAll(tmpDir)
	}, nil
}
