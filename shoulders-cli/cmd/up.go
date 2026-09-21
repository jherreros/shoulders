package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/airgap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/flux"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/manifests"
	"github.com/jherreros/shoulders/shoulders-cli/internal/tui"
	"github.com/spf13/cobra"
)

var (
	upClusterName string
	upVerbose     bool
	upLocal       bool
	upBundle      string
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Create the local cluster and install platform addons",
	RunE: func(cmd *cobra.Command, args []string) error {
		if upLocal && upBundle != "" {
			return fmt.Errorf("cannot combine --local and --bundle")
		}
		if upBundle != "" && currentConfig.Provider() == config.ProviderExisting {
			return fmt.Errorf("--bundle on provider existing requires pre-mirrored images; automatic image import only supports vind")
		}
		clusterName := configuredClusterName(cmd, "name", upClusterName)
		profileSpec := currentConfig.ProfileSpec()
		publicConfig := bootstrap.PublicDomainConfig{
			DexHost:          currentConfig.DexHost(),
			GrafanaHost:      currentConfig.GrafanaHost(),
			HeadlampHost:     currentConfig.HeadlampHost(),
			ReporterHost:     currentConfig.ReporterHost(),
			PrometheusHost:   currentConfig.PrometheusHost(),
			AlertmanagerHost: currentConfig.AlertmanagerHost(),
			HubbleHost:       currentConfig.HubbleHost(),
		}
		var err error
		if currentConfig.HasCustomDomain() {
			publicConfig.TLS, err = bootstrap.GenerateDexTLSMaterial(publicConfig.DexHost)
		} else {
			publicConfig.TLS, err = bootstrap.DefaultDexTLSMaterial()
		}
		if err != nil {
			return fmt.Errorf("prepare dex tls material: %w", err)
		}

		authConfig := manifests.AuthenticationConfig
		if currentConfig.HasCustomDomain() {
			authConfig = bootstrap.RenderAuthenticationConfig(publicConfig.DexHost, publicConfig.TLS.CAPEM)
		}

		tracker := tui.NewPhaseTracker(upPhases(), upVerbose)
		defer tracker.Stop()

		// Phase 1: cluster preparation
		if currentConfig.Provider() == config.ProviderExisting {
			tracker.Start(verboseDetail("connecting to existing cluster context %q", currentConfig.Cluster.Context))
			if err := bootstrap.EnsureExistingCluster(cmd.Context(), kubeconfig); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to connect to existing cluster: %w", err)
			}
		} else {
			tracker.Start(verboseDetail("creating vind cluster %q using %s profile", clusterName, profileSpec.Name))
			if err := bootstrap.EnsureVindCluster(cmd.Context(), clusterName, manifests.VindConfigForProfile(profileSpec.Name), authConfig, publicConfig.DexHost); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to create vind cluster: %w", err)
			}
			// vind freezes cluster DNS state at creation; reconcile
			// forwarding right away so later phases never depend on
			// stale upstreams. Best effort only.
			if changed, err := bootstrap.EnsureClusterDNS(cmd.Context(), kubeconfig); err != nil {
				tracker.UpdateDetail(verboseDetail("cluster DNS reconcile warning: %v", err))
			} else if changed {
				tracker.UpdateDetail(verboseDetail("reconciled cluster DNS forwarding"))
			}
		}
		tracker.Complete()

		// Bundle preparation: extract once, import all images directly into
		// the vind nodes before anything is installed, so no workload ever
		// pulls from the internet. Direct ctr import (with retries) is used
		// instead of the local registry on purpose: on a fresh cluster the
		// nodes are tainted until Cilium is up, the registry PVC cannot bind
		// until the provisioner schedules, and node containers cannot
		// resolve cluster DNS — the registry is only usable after Cilium,
		// which itself needs its images first.
		var bundleMeta *airgap.BundleMeta
		var bundleDir string
		var bundleCleanup func()
		if upBundle != "" {
			tracker.Start(verboseDetail("extracting airgap bundle and importing images"))
			extractDir, err := airgap.ExtractBundle(upBundle)
			if err != nil {
				tracker.Fail(err.Error())
				return err
			}
			bundleDir = extractDir
			bundleCleanup = func() {
				os.RemoveAll(extractDir) //nolint:errcheck // best-effort temp cleanup
			}
			defer bundleCleanup()
			meta, err := airgap.ReadBundleMeta(extractDir)
			if err != nil {
				tracker.Fail(err.Error())
				return err
			}
			bundleMeta = meta
			tars, err := airgap.BundleImageTars(extractDir, len(meta.Images))
			if err != nil {
				tracker.Fail(err.Error())
				return err
			}
			tracker.UpdateDetail(verboseDetail("importing %d container images directly", len(tars)))
			if err := airgap.ImportImageTars(cmd.Context(), clusterName, tars, func(message string) {
				tracker.UpdateDetail(verboseDetail("%s", message))
			}); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("import bundle images: %w", err)
			}
			tracker.Complete()
		}

		// Phase 2: networking prerequisites
		detail := verboseDetail("installing Gateway API CRDs")
		if currentConfig.CiliumEnabled() {
			detail = verboseDetail("installing Gateway API CRDs and Cilium Helm chart with gatewayAPI")
		}
		tracker.Start(detail)
		// Install Gateway API CRDs before Cilium so the operator can
		// register the GatewayClass controller on startup.
		if err := kube.ApplyManifest(cmd.Context(), kubeconfig, manifests.GatewayAPICRDs, ""); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to install gateway api crds: %w", err)
		}
		if currentConfig.CiliumEnabled() {
			if bundleMeta != nil {
				ciliumChart, err := bundleMeta.FindChart("cilium", currentConfig.CiliumVersion())
				if err != nil {
					tracker.Fail(err.Error())
					return fmt.Errorf("bundle cilium chart: %w", err)
				}
				if err := bootstrap.EnsureCiliumWithChart(kubeconfig, currentConfig.CiliumVersion(), bootstrap.CiliumOptionsForProfile(profileSpec.Name), filepath.Join(bundleDir, airgap.BundleChartsDir, ciliumChart.File)); err != nil {
					tracker.Fail(err.Error())
					return fmt.Errorf("failed to install cilium: %w", err)
				}
			} else if err := bootstrap.EnsureCilium(kubeconfig, currentConfig.CiliumVersion(), bootstrap.CiliumOptionsForProfile(profileSpec.Name)); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to install cilium: %w", err)
			}
			if err := bootstrap.RestartStuckPods(kubeconfig); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to restart stuck pods: %w", err)
			}
		}
		tracker.Complete()

		// Phase 3: Flux install
		tracker.Start(verboseDetail("downloading Flux install manifest and applying source + Kustomizations"))
		fluxSource := bootstrap.FluxSourceFromConfig(currentConfig, profileSpec.Name)
		fluxManifest := []byte(nil)
		if upLocal {
			tracker.UpdateDetail(verboseDetail("snapshotting local working tree and pushing OCI artifact"))
			localSource, _, err := prepareLocalOCISource(cmd.Context(), profileSpec.Name)
			if err != nil {
				tracker.Fail(err.Error())
				return err
			}
			fluxSource = localSource
		}
		if bundleMeta != nil {
			var err error
			fluxSource, fluxManifest, err = prepareBundleFluxSource(cmd.Context(), tracker, bundleMeta, bundleDir, profileSpec.Name)
			if err != nil {
				tracker.Fail(err.Error())
				return err
			}
		}
		if fluxManifest != nil {
			if err := bootstrap.EnsureFluxWithManifest(context.Background(), kubeconfig,
				fluxSource,
				publicConfig,
				fluxManifest,
			); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to install flux: %w", err)
			}
		} else if err := bootstrap.EnsureFlux(context.Background(), kubeconfig,
			fluxSource,
			publicConfig,
		); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to install flux: %w", err)
		}
		if upLocal {
			if err := saveCurrentConfig(); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to persist local OCI source: %w", err)
			}
		}
		// Always suspend the Flux-managed Cilium HelmRelease. When Cilium is
		// enabled, the CLI manages the installation directly; when it is
		// disabled, this keeps Flux from installing it implicitly.
		if err := bootstrap.SuspendCiliumHelmRelease(kubeconfig); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to suspend cilium helmrelease: %w", err)
		}
		tracker.Complete()

		// Phase 4: Flux reconciliation
		tracker.Start("waiting for kustomizations...")
		if err := waitForFluxTUI(tracker); err != nil {
			tracker.Fail(err.Error())
			return err
		}
		// Delete any pods stuck in ContainerCreating from the initial
		// Flux reconciliation. The burst of pod creation can overwhelm
		// Cilium's endpoint API, leaving pods in exponential backoff.
		if currentConfig.CiliumEnabled() {
			if err := bootstrap.RestartStuckPods(kubeconfig); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to restart stuck pods: %w", err)
			}
			// Cilium replaces kube-proxy: wait for the agents so service
			// routing is programmed before Flux and workloads start.
			tracker.UpdateDetail(verboseDetail("waiting for cilium agents"))
			if err := bootstrap.WaitForDaemonSetReady(kubeconfig, "kube-system", "cilium", 10*time.Minute); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("wait for cilium agents: %w", err)
			}
		}
		tracker.Complete()

		// Phase 5: Platform deployments
		deployments := platformDeploymentsForProfile(profileSpec)
		tracker.Start(verboseDetail("waiting for %d deployments plus Garage object storage", len(deployments)))
		for _, d := range deployments {
			tracker.UpdateDetail(fmt.Sprintf("waiting for %s/%s", d.ns, d.name))
			if err := bootstrap.WaitForDeploymentReady(kubeconfig, d.ns, d.name, 10*time.Minute); err != nil {
				tracker.Fail(fmt.Sprintf("%s/%s not ready", d.ns, d.name))
				return fmt.Errorf("failed waiting for %s deployment: %w", d.name, err)
			}
		}
		tracker.UpdateDetail("waiting for garage/garage StatefulSet")
		if err := bootstrap.WaitForStatefulSetReady(kubeconfig, "garage", "garage", 10*time.Minute); err != nil {
			tracker.Fail("garage/garage not ready")
			return fmt.Errorf("failed waiting for garage statefulset: %w", err)
		}
		tracker.UpdateDetail("waiting for garage layout initialization")
		if err := bootstrap.WaitForJobComplete(kubeconfig, "garage", "garage-layout-init", 10*time.Minute); err != nil {
			tracker.Fail("garage layout not initialized")
			return fmt.Errorf("failed waiting for garage layout initialization: %w", err)
		}
		tracker.Complete()

		// Phase 6: Gateway routes
		tracker.Start(verboseDetail("resolving HTTPRoutes"))
		if gatewayChecksRequired() {
			routes := gatewayRoutesForProfile(profileSpec)
			for _, r := range routes {
				tracker.UpdateDetail(fmt.Sprintf("waiting for %s/%s HTTPRoute", r.ns, r.name))
				if err := bootstrap.WaitForHTTPRouteResolved(kubeconfig, r.ns, r.name, 5*time.Minute); err != nil {
					tracker.Fail(fmt.Sprintf("%s route not resolved", r.name))
					return fmt.Errorf("failed waiting for %s route: %w", r.name, err)
				}
			}
		} else {
			tracker.UpdateDetail(verboseDetail("skipping HTTPRoute checks because cilium is disabled"))
		}
		tracker.Complete()

		// Phase 7: Status validation
		tracker.Start(verboseDetail("waiting for shoulders status to report all systems healthy"))
		if err := waitForHealthyStatus(cmd.Context(), 5*time.Minute); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to validate cluster status: %w", err)
		}
		tracker.Complete()

		fmt.Println()
		fmt.Println(tracker.Summary())
		fmt.Println()
		return nil
	},
}

type namedPlatformResource struct {
	ns   string
	name string
}

func platformDeploymentsForProfile(profile config.ProfileSpec) []namedPlatformResource {
	deployments := []namedPlatformResource{
		{"dex", "dex"},
		{"headlamp", "headlamp"},
		{"observability", "kube-prometheus-stack-grafana"},
	}
	if profile.PolicyReporter {
		deployments = append(deployments, namedPlatformResource{"policy-reporter", "policy-reporter-ui"})
	}
	return deployments
}

func gatewayRoutesForProfile(profile config.ProfileSpec) []namedPlatformResource {
	routes := []namedPlatformResource{
		{"dex", "dex"},
		{"headlamp", "headlamp"},
		{"observability", "grafana"},
	}
	if profile.PolicyReporter {
		routes = append(routes, namedPlatformResource{"policy-reporter", "policy-reporter"})
	}
	return routes
}

func upPhases() []string {
	phaseOne := "Create vind cluster"
	if currentConfig != nil && currentConfig.Provider() == config.ProviderExisting {
		phaseOne = "Connect to existing cluster"
	}
	phaseTwo := "Install Gateway API CRDs"
	if currentConfig != nil && currentConfig.CiliumEnabled() {
		phaseTwo = "Install Cilium CNI"
	}
	phases := []string{
		phaseOne,
	}
	if upBundle != "" {
		phases = append(phases, "Import airgap bundle")
	}
	return append(phases,
		phaseTwo,
		"Install Flux CD",
		"Reconcile Flux kustomizations",
		"Wait for platform deployments",
		"Configure gateway routes",
		"Validate cluster status",
	)
}

// verboseDetail returns detail only when --verbose is set.
func verboseDetail(format string, a ...any) string {
	if !upVerbose {
		return ""
	}
	return fmt.Sprintf(format, a...)
}

func waitForFluxTUI(tracker *tui.PhaseTracker) error {
	ctx := context.Background()
	client, err := kube.NewDynamicClient(kubeconfig)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	// A fully cold Docker engine must pull the complete addon image set,
	// which can push the initial Flux reconciliation past 10 minutes.
	timeout := time.After(20 * time.Minute)
	var lastErr error
	lastReconcileRequest := map[string]time.Time{}

	for {
		select {
		case <-ticker.C:
			if sources, err := flux.PendingSources(ctx, client, "flux-system"); err == nil {
				if failed, ok := flux.FirstSourcePullFailure(sources); ok {
					return fmt.Errorf("flux source %s/%s cannot be fetched: %s%s", failed.Kind, failed.Name, failed.Message, fluxSourceHint())
				}
			}
			pending, err := flux.PendingKustomizations(ctx, client, "flux-system")
			if err != nil {
				lastErr = err
				tracker.UpdateDetail(fmt.Sprintf("waiting for flux api: %v", err))
				continue
			}
			lastErr = nil
			if len(pending) == 0 {
				return nil
			}
			if failed, ok := flux.FirstMissingPathFailure(pending); ok {
				return fmt.Errorf("flux kustomization %q cannot find its configured path: %s%s", failed.Name, failed.Message, fluxSourceHint())
			}
			now := time.Now()
			for _, item := range pending {
				name := item.Name
				if last, ok := lastReconcileRequest[name]; ok && now.Sub(last) < 30*time.Second {
					continue
				}
				if err := flux.RequestKustomizationReconcile(ctx, client, "flux-system", name, now); err != nil {
					lastErr = err
					tracker.UpdateDetail(fmt.Sprintf("requesting %s reconcile: %v", name, err))
					continue
				}
				lastReconcileRequest[name] = now
			}
			tracker.UpdateDetail(fmt.Sprintf("pending: %s", flux.FormatPending(pending)))
		case <-timeout:
			if lastErr != nil {
				return lastErr
			}
			return fmt.Errorf("timed out waiting for Flux Kustomizations")
		}
	}
}

func fluxSourceHint() string {
	if currentConfig == nil {
		return ""
	}
	if currentConfig.FluxSource() == config.FluxSourceOCI {
		if currentConfig.FluxOCIURL() == "" || currentConfig.FluxOCITag() == "" {
			return ". The Flux source is oci but platform.flux.ociRepository.url/tag is not set; pass --set platform.flux.ociRepository.url=oci://<registry>/<repo> and --set platform.flux.ociRepository.tag=<immutable-tag>"
		}
		return fmt.Sprintf(". The Flux source is %s:%s; verify the artifact was pushed there with an immutable tag (not latest) and the cluster can reach the registry", currentConfig.FluxOCIURL(), currentConfig.FluxOCITag())
	}
	if currentConfig.FluxRepositoryURL() != config.DefaultFluxRepoURL || currentConfig.FluxRepositoryBranch() != config.DefaultFluxBranch {
		return ""
	}
	return fmt.Sprintf(". The default Flux source is %s@%s; if you are validating local changes that are not pushed there, pass --set platform.flux.gitRepository.url=<repo-url> and --set platform.flux.gitRepository.branch=<branch>", config.DefaultFluxRepoURL, config.DefaultFluxBranch)
}

func waitForHealthyStatus(ctx context.Context, timeout time.Duration) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		summary, err := gatherStatus(deadlineCtx)
		if err != nil {
			lastErr = err
		} else {
			podsHealthy := summary.TotalPods > 0 && summary.HealthyPods == summary.TotalPods
			if summary.NodesReady && podsHealthy && summary.FluxReady && summary.XPlaneReady && summary.GatewayReady {
				return nil
			}
		}

		select {
		case <-deadlineCtx.Done():
			if lastErr != nil {
				return lastErr
			}
			return fmt.Errorf("timed out waiting for shoulders status to report all systems healthy")
		case <-ticker.C:
		}
	}
}

func init() {
	upCmd.Flags().StringVar(&upClusterName, "name", bootstrap.DefaultClusterName, "Name of the cluster to create when provider=vind")
	upCmd.Flags().BoolVarP(&upVerbose, "verbose", "v", false, "Show detailed progress information for each phase")
	upCmd.Flags().BoolVar(&upLocal, "local", false, "Snapshot the local working tree (including uncommitted changes) into an OCI artifact and install Flux from it instead of git")
	upCmd.Flags().StringVar(&upBundle, "bundle", "", "Install from a self-contained airgap bundle file (see 'shoulders vendor') instead of the internet")
}
