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

	// WorkloadTier marks a cluster that runs the workload tier rather than
	// the platform tier. It changes which kustomizations are created and
	// which resources are removed from them.
	WorkloadTier bool

	// SopsAgeKey is the age identity flux uses to decrypt *.sops.yaml. Empty
	// leaves decryption off: no Secret is created and no Kustomization asks
	// for one, so a topology that does not use SOPS installs exactly as
	// before.
	SopsAgeKey []byte
}

// FluxSourceFromConfig builds the FluxSource for an install from CLI config.
// FluxConfigSource is the subset of configuration FluxSource needs.
//
// Both *config.Config and *config.ResolvedCluster satisfy it, which lets a
// per-cluster install use its own resolved Flux settings (path prefix, source)
// without duplicating this mapping.
type FluxConfigSource interface {
	FluxSource() string
	FluxRepositoryURL() string
	FluxRepositoryBranch() string
	FluxOCIURL() string
	FluxOCITag() string
	FluxOCIInsecure() bool
	FluxPathPrefix() string
}

func FluxSourceFromConfig(cfg FluxConfigSource, profile string) FluxSource {
	return FluxSourceFromConfigTier(cfg, profile, false)
}

// FluxSourceFromConfigTier is FluxSourceFromConfig with the tier.
func FluxSourceFromConfigTier(cfg FluxConfigSource, profile string, workloadTier bool) FluxSource {
	if cfg == nil {
		return FluxSource{Kind: config.FluxSourceGit, PathPrefix: ".", Profile: profile}
	}
	src := FluxSource{
		Kind:         cfg.FluxSource(),
		GitURL:       cfg.FluxRepositoryURL(),
		GitBranch:    cfg.FluxRepositoryBranch(),
		OCIURL:       cfg.FluxOCIURL(),
		OCITag:       cfg.FluxOCITag(),
		OCIInsecure:  cfg.FluxOCIInsecure(),
		PathPrefix:   cfg.FluxPathPrefix(),
		Profile:      profile,
		WorkloadTier: workloadTier,
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
	if err := applyFluxKustomizations(ctx, kubeconfigPath, source); err != nil {
		return err
	}
	return nil
}

// applyFluxKustomizations applies the decryption material (when the source
// carries an age identity) and then the tier's Kustomization set.
//
// Order matters: a Kustomization with `decryption.provider: sops` cannot
// become Ready until the Secret it references exists.
func applyFluxKustomizations(ctx context.Context, kubeconfigPath string, source FluxSource) error {
	options := []fluxManifestOption(nil)
	if len(source.SopsAgeKey) > 0 {
		secret, err := sopsAgeSecretManifest(source.SopsAgeKey)
		if err != nil {
			return fmt.Errorf("render the sops age key: %w", err)
		}
		if err := kube.ApplyManifest(ctx, kubeconfigPath, secret, "flux-system"); err != nil {
			return fmt.Errorf("apply the sops age key: %w", err)
		}
		options = append(options, withSopsDecryption())
	}
	manifest := fluxKustomizationsManifestForSource(
		source.PathPrefix, source.Profile, source.sourceRefKind(), source.WorkloadTier, options...)
	if err := kube.ApplyManifest(ctx, kubeconfigPath, manifest, "flux-system"); err != nil {
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
			if err := kube.DeleteManifest(ctx, kubeconfigPath, fluxKustomizationsManifestForSource(pathPrefix, profile, sourceRefKind, false), "flux-system"); err != nil {
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
	return applyFluxKustomizations(ctx, kubeconfigPath, source)
}

func fluxKustomizationsManifest(pathPrefix, profile string) []byte {
	return fluxKustomizationsManifestForSource(pathPrefix, profile, "GitRepository", false)
}

// fluxManifestOptions are the render-time switches that are not part of the
// cluster's identity. Kept as options so the many call sites that only care
// about the tier do not have to pass them.
type fluxManifestOptions struct {
	decrypt bool
}

type fluxManifestOption func(*fluxManifestOptions)

// withSopsDecryption asks flux to decrypt *.sops.yaml with the sops-age Secret.
func withSopsDecryption() fluxManifestOption {
	return func(options *fluxManifestOptions) { options.decrypt = true }
}

func fluxKustomizationsManifestForSource(pathPrefix, profile, sourceRefKind string, workloadTier bool, opts ...fluxManifestOption) []byte {
	items := fluxKustomizationsForCluster(pathPrefix, profile, workloadTier)
	if sourceRefKind == "" {
		sourceRefKind = "GitRepository"
	}
	options := fluxManifestOptions{}
	for _, opt := range opts {
		opt(&options)
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
		if options.decrypt {
			builder.WriteString("  decryption:\n")
			builder.WriteString("    provider: sops\n")
			builder.WriteString("    secretRef:\n")
			builder.WriteString("      name: sops-age\n")
		}
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
		if len(item.Patches) > 0 {
			builder.WriteString("  patches:\n")
			for _, patch := range item.Patches {
				target, _ := patch.(map[string]interface{})["target"].(map[string]interface{})
				body, _ := patch.(map[string]interface{})["patch"].(string)
				builder.WriteString("    - target:\n")
				fmt.Fprintf(&builder, "        kind: %s\n", target["kind"])
				fmt.Fprintf(&builder, "        name: %s\n", target["name"])
				if namespace, ok := target["namespace"]; ok && namespace != "" {
					fmt.Fprintf(&builder, "        namespace: %s\n", namespace)
				}
				builder.WriteString("      patch: |\n")
				for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
					fmt.Fprintf(&builder, "        %s\n", line)
				}
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
		items = []fluxKustomization{items[0], items[1], items[2], items[3], items[4], items[5], items[6]}
	case config.ProfileLarge:
		items[4].Path = path("2-addons/profiles/large/helm-releases")
	}

	// User space is appended after the profile switch, not part of it: every
	// profile delivers the same tenant manifests, and the small profile rebuilds
	// the list above by index.
	//
	// Two Kustomizations, in this order, because a Workspace owns its namespace:
	// the tenant's namespaced XRs cannot be applied until the Workspace that
	// creates that namespace is ready.
	items = append(items,
		fluxKustomization{
			Name:      "user-space-workspaces",
			Path:      path("3-user-space/workspaces"),
			Wait:      true,
			DependsOn: []string{"crossplane", "helm-releases"},
		},
		fluxKustomization{
			Name:       "user-space-apps",
			Path:       path("3-user-space/team-a"),
			Substitute: true,
			DependsOn:  []string{"user-space-workspaces"},
		},
	)

	return items
}

// fluxKustomizationsForCluster is the kustomization set a cluster reconciles.
//
// A workload-tier cluster does not create the kustomizations that exist only
// for the platform tier. The resources that live in the shared kustomizations
// are removed by the delete patches SetClusterTier applies, which is a
// separate step because it has to happen after flux has created the
// kustomizations.
func fluxKustomizationsForCluster(pathPrefix, profile string, workloadTier bool) []fluxKustomization {
	items := fluxKustomizationsForProfile(pathPrefix, profile)
	if !workloadTier {
		// The mirror image of the loop below: the hub removes what exists only
		// for the workload tier.
		for i := range items {
			if resources, ok := workloadTierResources[items[i].Name]; ok {
				items[i].Patches = append(items[i].Patches, tierDeletePatches(resources)...)
			}
		}
		return items
	}
	kept := make([]fluxKustomization, 0, len(items))
	removed := map[string]bool{}
	for _, item := range items {
		if isPlatformTierKustomization(item.Name) {
			removed[item.Name] = true
			continue
		}
		if resources, ok := platformTierResources[item.Name]; ok {
			item.Patches = tierDeletePatches(resources)
		}
		if patches, ok := workloadTierJSONPatches[item.Name]; ok {
			item.Patches = append(item.Patches, tierJSONPatches(patches)...)
		}
		if item.Name == "helm-releases" {
			item.Patches = append(item.Patches, tierDependencyPatches(workloadTierReleaseDependencies)...)
			item.Patches = append(item.Patches, tierValuePatches(workloadTierValueOverrides)...)
		}
		kept = append(kept, item)
	}

	// Drop dependencies on the kustomizations that were removed. A dependency
	// on a kustomization that does not exist keeps the dependent one stuck:
	//
	//   dependency 'flux-system/headlamp' not found
	//
	// which blocks the whole tier, not just the dependent kustomization.
	for i := range kept {
		if len(removed) == 0 || len(kept[i].DependsOn) == 0 {
			continue
		}
		dependencies := make([]string, 0, len(kept[i].DependsOn))
		for _, dependency := range kept[i].DependsOn {
			if removed[dependency] {
				continue
			}
			dependencies = append(dependencies, dependency)
		}
		kept[i].DependsOn = dependencies
	}
	return kept
}

// isPlatformTierKustomization reports whether a flux kustomization exists only
// for the platform tier.
func isPlatformTierKustomization(name string) bool {
	for _, tier := range platformTierKustomizations {
		if tier == name {
			return true
		}
	}
	return false
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

	// Patches are applied by flux to the built manifests. They carry the tier
	// exclusions: a workload-tier cluster removes the platform tier from the
	// shared kustomizations.
	//
	// They are part of the object the CLI applies, not a patch applied
	// afterwards, so flux cannot reconcile the platform tier in the window
	// between creating the Kustomization and patching it.
	Patches []interface{}
}

func fluxPlatformConfigManifest(profile string, publicConfig PublicDomainConfig) []byte {
	profileSpec := config.ProfileSpecFor(profile)
	values := map[string]string{
		"SHOULDERS_DEX_HOST":                  publicConfig.DexHost,
		"SHOULDERS_GRAFANA_HOST":              publicConfig.GrafanaHost,
		"SHOULDERS_HEADLAMP_HOST":             publicConfig.HeadlampHost,
		"SHOULDERS_HEADLAMP_CALLBACK_URL":     publicConfig.HeadlampCallbackURL,
		"SHOULDERS_HEADLAMP_CLIENT_ID":        publicConfig.HeadlampClientID,
		"SHOULDERS_HEADLAMP_CLIENT_SECRET":    publicConfig.HeadlampClientSecret,
		"SHOULDERS_WORKLOAD_DOMAIN":           publicConfig.WorkloadDomain,
		"SHOULDERS_CLUSTER_NAME":              publicConfig.ClusterName,
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
		"SHOULDERS_HEADLAMP_CALLBACK_URL",
		"SHOULDERS_HEADLAMP_CLIENT_ID",
		"SHOULDERS_HEADLAMP_CLIENT_SECRET",
		"SHOULDERS_WORKLOAD_DOMAIN",
		"SHOULDERS_CLUSTER_NAME",
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
		for _, name := range []string{"helm-repositories", "namespaces", "crds", "helm-releases", "crossplane", "gateway", "policy-reporter", "trivy-dashboard", "headlamp", "user-space-workspaces", "user-space-apps"} {
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
