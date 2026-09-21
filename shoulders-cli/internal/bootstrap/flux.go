package bootstrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

const FluxInstallURL = "https://github.com/fluxcd/flux2/releases/download/v2.8.3/install.yaml"

const fluxPlatformConfigName = "shoulders-platform-config"

var fluxKustomizationGVR = schema.GroupVersionResource{
	Group:    "kustomize.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "kustomizations",
}

var fluxGitRepositoryGVR = schema.GroupVersionResource{
	Group:    "source.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "gitrepositories",
}

var fluxOCIRepositoryGVR = schema.GroupVersionResource{
	Group:    "source.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "ocirepositories",
}

// FluxSource selects which Flux source controller object the Shoulders
// Kustomizations reconcile from. Kind is config.FluxSourceGit ("git") or
// config.FluxSourceOCI ("oci"). The OCI artifact carries the same directory
// layout as the git repo, so Kustomization paths are identical — only
// sourceRef.kind and the managed source object differ.
type FluxSource struct {
	Kind        string
	GitURL      string
	GitBranch   string
	OCIURL      string
	OCITag      string
	OCIInsecure bool
	PathPrefix  string
	Profile     string
}

// FluxSourceFromConfig builds the FluxSource for an install from CLI config.
func FluxSourceFromConfig(cfg *config.Config, profile string) FluxSource {
	if cfg == nil {
		return FluxSource{Kind: config.FluxSourceGit, PathPrefix: ".", Profile: profile}
	}
	src := FluxSource{
		Kind:        cfg.FluxSource(),
		GitURL:      cfg.FluxRepositoryURL(),
		GitBranch:   cfg.FluxRepositoryBranch(),
		OCIURL:      cfg.FluxOCIURL(),
		OCITag:      cfg.FluxOCITag(),
		OCIInsecure: cfg.FluxOCIInsecure(),
		PathPrefix:  cfg.FluxPathPrefix(),
		Profile:     profile,
	}
	if src.Kind == "" {
		src.Kind = config.FluxSourceGit
	}
	if src.PathPrefix == "" {
		src.PathPrefix = "."
	}
	return src
}

func (s FluxSource) sourceRefKind() string {
	if s.Kind == config.FluxSourceOCI {
		return "OCIRepository"
	}
	return "GitRepository"
}

func EnsureFlux(ctx context.Context, kubeconfigPath string, source FluxSource, publicConfig PublicDomainConfig) error {
	manifest, err := DownloadFluxManifest(ctx)
	if err != nil {
		return err
	}
	return EnsureFluxWithManifest(ctx, kubeconfigPath, source, publicConfig, manifest)
}

// EnsureFluxWithManifest installs Flux like EnsureFlux but applies a
// pre-fetched install manifest (airgap installs vendor it into the bundle)
// instead of downloading it.
func EnsureFluxWithManifest(ctx context.Context, kubeconfigPath string, source FluxSource, publicConfig PublicDomainConfig, manifest []byte) error {
	if err := kube.ApplyManifest(ctx, kubeconfigPath, manifest, ""); err != nil {
		return fmt.Errorf("apply flux install manifest: %w", err)
	}
	if err := kube.ApplyManifest(ctx, kubeconfigPath, fluxPlatformConfigManifest(source.Profile, publicConfig), "flux-system"); err != nil {
		return fmt.Errorf("apply flux platform config: %w", err)
	}
	if err := kube.ApplyManifest(ctx, kubeconfigPath, fluxSourceManifest(source), "flux-system"); err != nil {
		return fmt.Errorf("apply flux source: %w", err)
	}
	if err := deleteInactiveFluxSource(ctx, kubeconfigPath, source); err != nil {
		return err
	}
	if err := kube.ApplyManifest(ctx, kubeconfigPath, fluxKustomizationsManifestForSource(source.PathPrefix, source.Profile, source.sourceRefKind()), "flux-system"); err != nil {
		return fmt.Errorf("apply flux config: %w", err)
	}
	return nil
}

func UninstallShouldersFlux(ctx context.Context, kubeconfigPath, pathPrefix string) error {
	installed, err := fluxAPIsInstalled(kubeconfigPath)
	if err != nil {
		return err
	}
	if !installed {
		return nil
	}

	for _, profile := range []string{config.ProfileSmall, config.ProfileMedium, config.ProfileLarge} {
		for _, sourceRefKind := range []string{"GitRepository", "OCIRepository"} {
			if err := kube.DeleteManifest(ctx, kubeconfigPath, fluxKustomizationsManifestForSource(pathPrefix, profile, sourceRefKind), "flux-system"); err != nil {
				return fmt.Errorf("delete flux kustomizations for profile %s source %s: %w", profile, sourceRefKind, err)
			}
		}
	}
	if err := waitForFluxKustomizationsDeleted(ctx, kubeconfigPath); err != nil {
		return err
	}
	if err := kube.DeleteManifest(ctx, kubeconfigPath, fluxPlatformConfigManifest(config.ProfileMedium, PublicDomainConfig{}), "flux-system"); err != nil {
		return fmt.Errorf("delete flux platform config: %w", err)
	}
	if err := deleteFluxSourceObject(ctx, kubeconfigPath, fluxGitRepositoryGVR); err != nil {
		return fmt.Errorf("delete flux git repository: %w", err)
	}
	if err := deleteFluxSourceObject(ctx, kubeconfigPath, fluxOCIRepositoryGVR); err != nil {
		return fmt.Errorf("delete flux oci repository: %w", err)
	}
	return nil
}

func deleteInactiveFluxSource(ctx context.Context, kubeconfigPath string, source FluxSource) error {
	if source.Kind == config.FluxSourceOCI {
		return deleteFluxSourceObject(ctx, kubeconfigPath, fluxGitRepositoryGVR)
	}
	return deleteFluxSourceObject(ctx, kubeconfigPath, fluxOCIRepositoryGVR)
}

func deleteFluxSourceObject(ctx context.Context, kubeconfigPath string, gvr schema.GroupVersionResource) error {
	client, err := kube.NewDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}
	err = client.Resource(gvr).Namespace("flux-system").Delete(ctx, "flux-system", metav1.DeleteOptions{})
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "could not find") || strings.Contains(strings.ToLower(err.Error()), "no matches") {
		return nil
	}
	return err
}

func fluxAPIsInstalled(kubeconfigPath string) (bool, error) {
	discoveryClient, err := kube.NewDiscoveryClient(kubeconfigPath)
	if err != nil {
		return false, fmt.Errorf("create discovery client: %w", err)
	}

	groups, err := discoveryClient.ServerGroups()
	if err != nil {
		return false, fmt.Errorf("discover api groups: %w", err)
	}

	hasKustomize := false
	hasSource := false
	for _, group := range groups.Groups {
		switch group.Name {
		case "kustomize.toolkit.fluxcd.io":
			hasKustomize = true
		case "source.toolkit.fluxcd.io":
			hasSource = true
		}
	}

	return hasKustomize && hasSource, nil
}

func fluxSourceManifest(source FluxSource) []byte {
	if source.Kind == config.FluxSourceOCI {
		return fluxOCIRepositoryManifest(source.OCIURL, source.OCITag, source.OCIInsecure)
	}
	return fluxGitRepositoryManifest(source.GitURL, source.GitBranch)
}

func fluxGitRepositoryManifest(repoURL, branch string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: flux-system
  namespace: flux-system
spec:
  interval: 1m
  url: %q
  ref:
    branch: %q
`, repoURL, branch))
}

func fluxOCIRepositoryManifest(ociURL, tag string, insecure bool) []byte {
	var builder strings.Builder
	builder.WriteString("apiVersion: source.toolkit.fluxcd.io/v1\n")
	builder.WriteString("kind: OCIRepository\n")
	builder.WriteString("metadata:\n")
	builder.WriteString("  name: flux-system\n")
	builder.WriteString("  namespace: flux-system\n")
	builder.WriteString("spec:\n")
	builder.WriteString("  interval: 1m\n")
	fmt.Fprintf(&builder, "  url: %q\n", strings.TrimSpace(ociURL))
	builder.WriteString("  ref:\n")
	fmt.Fprintf(&builder, "    tag: %q\n", strings.TrimSpace(tag))
	if insecure {
		builder.WriteString("  insecure: true\n")
	}
	return []byte(builder.String())
}

// ApplyFluxSourceAndKustomizations re-applies the Flux source object and the
// profile Kustomizations without reinstalling Flux itself. Used by `sync`
// after pushing a new OCI artifact tag.
func ApplyFluxSourceAndKustomizations(ctx context.Context, kubeconfigPath string, source FluxSource) error {
	if err := kube.ApplyManifest(ctx, kubeconfigPath, fluxSourceManifest(source), "flux-system"); err != nil {
		return fmt.Errorf("apply flux source: %w", err)
	}
	if err := deleteInactiveFluxSource(ctx, kubeconfigPath, source); err != nil {
		return err
	}
	if err := kube.ApplyManifest(ctx, kubeconfigPath, fluxKustomizationsManifestForSource(source.PathPrefix, source.Profile, source.sourceRefKind()), "flux-system"); err != nil {
		return fmt.Errorf("apply flux config: %w", err)
	}
	return nil
}

func fluxKustomizationsManifest(pathPrefix, profile string) []byte {
	return fluxKustomizationsManifestForSource(pathPrefix, profile, "GitRepository")
}

func fluxKustomizationsManifestForSource(pathPrefix, profile, sourceRefKind string) []byte {
	items := fluxKustomizationsForProfile(pathPrefix, profile)
	if sourceRefKind == "" {
		sourceRefKind = "GitRepository"
	}

	var builder strings.Builder
	for index, item := range items {
		if index > 0 {
			builder.WriteString("---\n")
		}
		builder.WriteString("apiVersion: kustomize.toolkit.fluxcd.io/v1\n")
		builder.WriteString("kind: Kustomization\n")
		builder.WriteString("metadata:\n")
		fmt.Fprintf(&builder, "  name: %s\n", item.Name)
		builder.WriteString("  namespace: flux-system\n")
		builder.WriteString("spec:\n")
		builder.WriteString("  interval: 10m\n")
		fmt.Fprintf(&builder, "  path: %s\n", item.Path)
		builder.WriteString("  prune: true\n")
		if item.Wait {
			builder.WriteString("  wait: true\n")
		}
		builder.WriteString("  sourceRef:\n")
		fmt.Fprintf(&builder, "    kind: %s\n", sourceRefKind)
		builder.WriteString("    name: flux-system\n")
		if item.Substitute {
			builder.WriteString("  postBuild:\n")
			builder.WriteString("    substituteFrom:\n")
			builder.WriteString("      - kind: ConfigMap\n")
			fmt.Fprintf(&builder, "        name: %s\n", fluxPlatformConfigName)
		}
		if len(item.DependsOn) > 0 {
			builder.WriteString("  dependsOn:\n")
			for _, dependency := range item.DependsOn {
				fmt.Fprintf(&builder, "    - name: %s\n", dependency)
			}
		}
	}
	return []byte(builder.String())
}

func fluxKustomizationsForProfile(pathPrefix, profile string) []fluxKustomization {
	path := func(relativePath string) string {
		return fluxRepoPath(pathPrefix, relativePath)
	}

	items := []fluxKustomization{
		{Name: "helm-repositories", Path: path("2-addons/manifests/helm-repositories"), Wait: true},
		{Name: "namespaces", Path: path("2-addons/manifests/namespaces"), Wait: true},
		{Name: "crds", Path: path("2-addons/manifests/crds"), Wait: true},
		{Name: "headlamp", Path: path("2-addons/manifests/headlamp"), Substitute: true, DependsOn: []string{"namespaces"}},
		{Name: "helm-releases", Path: path("2-addons/manifests/helm-releases"), Wait: true, Substitute: true, DependsOn: []string{"helm-repositories", "namespaces", "crds", "headlamp"}},
		{Name: "crossplane", Path: path("2-addons/manifests/crossplane"), Substitute: true, DependsOn: []string{"helm-releases"}},
		{Name: "gateway", Path: path("2-addons/manifests/gateway"), Substitute: true, DependsOn: []string{"namespaces"}},
		{Name: "policy-reporter", Path: path("2-addons/manifests/policy-reporter"), DependsOn: []string{"helm-releases"}},
		{Name: "trivy-dashboard", Path: path("2-addons/manifests/trivy-dashboard"), DependsOn: []string{"helm-releases"}},
	}

	switch config.ProfileSpecFor(profile).Name {
	case config.ProfileSmall:
		items[0].Path = path("2-addons/profiles/small/helm-repositories")
		items[1].Path = path("2-addons/profiles/small/namespaces")
		items[4].Path = path("2-addons/profiles/small/helm-releases")
		items[5].Path = path("2-addons/profiles/small/crossplane")
		items[6].Path = path("2-addons/profiles/small/gateway")
		return []fluxKustomization{items[0], items[1], items[2], items[3], items[4], items[5], items[6]}
	case config.ProfileLarge:
		items[4].Path = path("2-addons/profiles/large/helm-releases")
	}

	return items
}

func fluxRepoPath(prefix, relativePath string) string {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" || trimmed == "." {
		return "./" + relativePath
	}
	trimmed = strings.TrimSuffix(trimmed, "/")
	if strings.HasPrefix(trimmed, "./") {
		return trimmed + "/" + relativePath
	}
	return trimmed + "/" + relativePath
}

type fluxKustomization struct {
	Name       string
	Path       string
	DependsOn  []string
	Wait       bool
	Substitute bool
}

func fluxPlatformConfigManifest(profile string, publicConfig PublicDomainConfig) []byte {
	profileSpec := config.ProfileSpecFor(profile)
	values := map[string]string{
		"SHOULDERS_DEX_HOST":                  publicConfig.DexHost,
		"SHOULDERS_GRAFANA_HOST":              publicConfig.GrafanaHost,
		"SHOULDERS_HEADLAMP_HOST":             publicConfig.HeadlampHost,
		"SHOULDERS_REPORTER_HOST":             publicConfig.ReporterHost,
		"SHOULDERS_PROMETHEUS_HOST":           publicConfig.PrometheusHost,
		"SHOULDERS_ALERTMANAGER_HOST":         publicConfig.AlertmanagerHost,
		"SHOULDERS_HUBBLE_HOST":               publicConfig.HubbleHost,
		"SHOULDERS_PROFILE":                   profileSpec.Name,
		"SHOULDERS_POSTGRES_INSTANCES":        fmt.Sprintf("%d", profileSpec.PostgresInstances),
		"SHOULDERS_KAFKA_REPLICAS":            fmt.Sprintf("%d", profileSpec.KafkaReplicas),
		"SHOULDERS_KAFKA_MIN_ISR":             fmt.Sprintf("%d", profileSpec.KafkaMinISR),
		"SHOULDERS_KAFKA_STORAGE":             profileSpec.KafkaStorage,
		"SHOULDERS_PROMETHEUS_RETENTION":      profileSpec.PrometheusRetention,
		"SHOULDERS_PROMETHEUS_RETENTION_SIZE": profileSpec.PrometheusSize,
		"SHOULDERS_DEX_TLS_CERT_B64":          base64.StdEncoding.EncodeToString([]byte(publicConfig.TLS.CertPEM)),
		"SHOULDERS_DEX_TLS_KEY_B64":           base64.StdEncoding.EncodeToString([]byte(publicConfig.TLS.KeyPEM)),
		"SHOULDERS_DEX_CA_CERT_B64":           base64.StdEncoding.EncodeToString([]byte(publicConfig.TLS.CAPEM)),
	}
	keys := []string{
		"SHOULDERS_DEX_HOST",
		"SHOULDERS_GRAFANA_HOST",
		"SHOULDERS_HEADLAMP_HOST",
		"SHOULDERS_REPORTER_HOST",
		"SHOULDERS_PROMETHEUS_HOST",
		"SHOULDERS_ALERTMANAGER_HOST",
		"SHOULDERS_HUBBLE_HOST",
		"SHOULDERS_PROFILE",
		"SHOULDERS_POSTGRES_INSTANCES",
		"SHOULDERS_KAFKA_REPLICAS",
		"SHOULDERS_KAFKA_MIN_ISR",
		"SHOULDERS_KAFKA_STORAGE",
		"SHOULDERS_PROMETHEUS_RETENTION",
		"SHOULDERS_PROMETHEUS_RETENTION_SIZE",
		"SHOULDERS_DEX_TLS_CERT_B64",
		"SHOULDERS_DEX_TLS_KEY_B64",
		"SHOULDERS_DEX_CA_CERT_B64",
	}

	var builder strings.Builder
	builder.WriteString("apiVersion: v1\n")
	builder.WriteString("kind: ConfigMap\n")
	builder.WriteString("metadata:\n")
	fmt.Fprintf(&builder, "  name: %s\n", fluxPlatformConfigName)
	builder.WriteString("  namespace: flux-system\n")
	builder.WriteString("data:\n")
	for _, key := range keys {
		fmt.Fprintf(&builder, "  %s: %q\n", key, values[key])
	}
	return []byte(builder.String())
}

func waitForFluxKustomizationsDeleted(ctx context.Context, kubeconfigPath string) error {
	client, err := kube.NewDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, name := range []string{"helm-repositories", "namespaces", "crds", "helm-releases", "crossplane", "gateway", "policy-reporter", "trivy-dashboard", "headlamp"} {
			if _, err := client.Resource(fluxKustomizationGVR).Namespace("flux-system").Get(ctx, name, metav1.GetOptions{}); err == nil {
				return false, nil
			}
		}
		return true, nil
	})
}

func DownloadFluxManifest(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FluxInstallURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to download flux install manifest: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}
