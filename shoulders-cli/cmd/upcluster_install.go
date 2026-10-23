package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jherreros/shoulders/shoulders-cli/internal/airgap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/manifests"
	"github.com/jherreros/shoulders/shoulders-cli/internal/tui"
)

// upOneCluster runs the full install for a single resolved cluster.
//
// Extracted from the original single-cluster `up` so the same phases can run
// once per cluster in a topology. Everything cluster-scoped is derived from the
// passed cluster, never from the file-level config.
//
// plan carries the cross-cluster state. It is nil for a single-cluster install,
// and non-nil for a meshed topology so this cluster can seed the shared trust
// anchor and be installed with its ClusterMesh peers.
func upOneCluster(cmd *cobra.Command, cluster *config.ResolvedCluster, tracker *tui.PhaseTracker, plan *topologyPlan) error {
	targetCluster = cluster
	clusterName := cluster.Name
	profileSpec := targetCluster.ProfileSpec()
	publicConfig := bootstrap.PublicDomainConfig{
		DexHost:          targetCluster.DexHost(),
		GrafanaHost:      targetCluster.GrafanaHost(),
		HeadlampHost:     targetCluster.HeadlampHost(),
		ReporterHost:     targetCluster.ReporterHost(),
		PrometheusHost:   targetCluster.PrometheusHost(),
		AlertmanagerHost: targetCluster.AlertmanagerHost(),
		HubbleHost:       targetCluster.HubbleHost(),
	}
	if plan != nil {
		publicConfig = plan.publicConfigFor(targetCluster)
	}
	// The Dex auth overlay is rendered inside ensureClusterExists; only the
	// public host mapping is needed from here on, for Flux and the gateway.
	//
	// The TLS material comes from the plan and is NOT regenerated here. It has
	// to be one material for the whole topology: the hub's Dex serves a
	// certificate signed by this CA, and a spoke's Headlamp has to trust that
	// same CA to verify the issuer it is redirected to. Generating one per
	// cluster leaves the spoke unable to verify the hub — which is the whole of
	// the problem the shared CA solves.
	var err error
	if plan == nil {
		if targetCluster.HasCustomDomain() {
			publicConfig.TLS, err = bootstrap.GenerateDexTLSMaterial(publicConfig.DexHost)
		} else {
			publicConfig.TLS, err = bootstrap.DefaultDexTLSMaterial()
		}
		if err != nil {
			return fmt.Errorf("prepare dex tls material: %w", err)
		}
	}

	// Phase 1: cluster preparation. Already done for every cluster when a
	// topology is being installed; a no-op fast path when it exists.
	tracker.Start(verboseDetail("preparing cluster %q", clusterName))
	if err := ensureClusterExists(ctx(cmd), cluster, plan); err != nil {
		tracker.Fail(err.Error())
		return err
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
	if targetCluster.CiliumEnabled() {
		detail = verboseDetail("installing Gateway API CRDs and Cilium Helm chart with gatewayAPI")
	}
	tracker.Start(detail)
	// Install Gateway API CRDs before Cilium so the operator can
	// register the GatewayClass controller on startup.
	if err := kube.ApplyManifest(cmd.Context(), kubeconfig, manifests.GatewayAPICRDs, ""); err != nil {
		tracker.Fail(err.Error())
		return fmt.Errorf("failed to install gateway api crds: %w", err)
	}
	if targetCluster.CiliumEnabled() {
		// Seed the shared Cilium trust anchor BEFORE installing Cilium: the helm
		// certificate method mints a fresh CA per cluster unless one is seeded,
		// and replacing a CA afterwards invalidates certificates already issued
		// from it.
		if err := seedSharedCA(ctx(cmd), cluster, plan); err != nil {
			tracker.Fail(err.Error())
			return err
		}
		ciliumOptions := bootstrap.CiliumOptionsForProfile(profileSpec.Name)
		ciliumOptions.ContextName = targetClusterContextName()
		if plan != nil {
			ciliumOptions.Mesh = plan.optionsFor(cluster)
		}
		if bundleMeta != nil {
			ciliumChart, err := bundleMeta.FindChart("cilium", targetCluster.CiliumVersion())
			if err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("bundle cilium chart: %w", err)
			}
			if err := bootstrap.EnsureCiliumWithChart(kubeconfig, targetCluster.CiliumVersion(), ciliumOptions, filepath.Join(bundleDir, airgap.BundleChartsDir, ciliumChart.File)); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("failed to install cilium: %w", err)
			}
		} else if err := bootstrap.EnsureCilium(kubeconfig, targetCluster.CiliumVersion(), ciliumOptions); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to install cilium: %w", err)
		}
		if err := bootstrap.RestartStuckPods(kubeconfig); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to restart stuck pods: %w", err)
		}
		if bundleMeta != nil {
			// Bundle installs serve charts from the local registry in
			// Phase 3, which needs schedulable nodes (registry PVC) and
			// programmed ClusterIPs (kube-proxy replacement). Both only
			// hold once the Cilium agents are Ready.
			tracker.UpdateDetail(verboseDetail("waiting for cilium agents"))
			if err := bootstrap.WaitForDaemonSetReady(kubeconfig, "kube-system", "cilium", 10*time.Minute); err != nil {
				tracker.Fail(err.Error())
				return fmt.Errorf("wait for cilium agents: %w", err)
			}
		}
	}
	tracker.Complete()

	// Phase 3: Flux install
	tracker.Start(verboseDetail("downloading Flux install manifest and applying source + Kustomizations"))
	// The source is built from the *resolved cluster*, not the file-level
	// config: in a topology each cluster has its own path prefix and tier.
	fluxSource := bootstrap.FluxSourceFromConfigTier(cluster, profileSpec.Name,
		cluster.Tier() == config.TierWorkload)
	fluxManifest := []byte(nil)
	if upLocal {
		tracker.UpdateDetail(verboseDetail("snapshotting local working tree and pushing OCI artifact"))
		localSource, _, err := prepareLocalOCISource(cmd.Context(), profileSpec.Name,
			cluster.Tier() == config.TierWorkload)
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
	// The age identity, when one exists, travels with the source: flux needs
	// it before the first reconcile, not after.
	var sopsErr error
	fluxSource, sopsErr = attachSopsAgeKey(fluxSource)
	if sopsErr != nil {
		tracker.Fail(sopsErr.Error())
		return sopsErr
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
	// A workload-tier cluster does not reconcile the platform tier, and the
	// flux Kustomizations that exist only for it are not created. The delete
	// patches for the shared ones travel in the manifest applied above, so
	// flux cannot install the platform tier in the window between creating a
	// Kustomization and excluding it.
	if cluster.Tier() == config.TierWorkload {
		if err := bootstrap.RemovePlatformTierKustomizations(kubeconfig); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to remove the platform tier: %w", err)
		}
	} else {
		// The mirror image: user workloads are the workload tier's reason to
		// exist, so a hub does not reconcile them. A monocluster is a hub, and
		// this keeps it behaving exactly as it did before the split.
		if err := bootstrap.RemoveWorkloadTierKustomizations(kubeconfig); err != nil {
			tracker.Fail(err.Error())
			return fmt.Errorf("failed to remove the workload tier: %w", err)
		}
	}

	// Decide who owns the Cilium HelmRelease.
	//
	// When Cilium is enabled, Flux owns it: the CLI bootstraps the install
	// (Flux needs a CNI before it can reconcile anything) and Flux then adopts
	// that release, so Renovate's version bumps actually reach the cluster.
	// When Cilium is disabled — the default for `provider: existing` — the
	// cluster has its own CNI and Flux must not install one implicitly.
	fluxOwnsCilium := targetCluster.CiliumEnabled()
	if err := bootstrap.SetCiliumHelmReleaseFluxOwnership(kubeconfig, fluxOwnsCilium); err != nil {
		tracker.Fail(err.Error())
		return fmt.Errorf("failed to set cilium flux ownership: %w", err)
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
	if targetCluster.CiliumEnabled() {
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
	deployments := platformDeploymentsForTier(profileSpec, cluster.Tier())
	tracker.Start(verboseDetail("waiting for %d deployments plus Garage object storage", len(deployments)))
	for _, d := range deployments {
		tracker.UpdateDetail(fmt.Sprintf("waiting for %s/%s", d.ns, d.name))
		if err := bootstrap.WaitForDeploymentReady(kubeconfig, d.ns, d.name, 10*time.Minute); err != nil {
			tracker.Fail(fmt.Sprintf("%s/%s not ready", d.ns, d.name))
			return fmt.Errorf("failed waiting for %s deployment: %w", d.name, err)
		}
	}
	if cluster.Tier() == config.TierWorkload {
		// node-exporter is a DaemonSet, so it is not in the deployment list.
		// It is part of the standard spoke telemetry set; waiting here keeps
		// `up` from reporting success while the node metrics are still absent.
		tracker.UpdateDetail("waiting for observability/node-exporter")
		if err := bootstrap.WaitForDaemonSetReady(kubeconfig, "observability", "node-exporter", 10*time.Minute); err != nil {
			tracker.Fail("observability/node-exporter not ready")
			return fmt.Errorf("failed waiting for node-exporter daemonset: %w", err)
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
		routes := gatewayRoutesForTier(profileSpec, cluster.Tier())
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
}
