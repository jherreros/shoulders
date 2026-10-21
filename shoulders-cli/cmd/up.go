package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/flux"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
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
	Long: `Create the local cluster and install platform addons.

Pre-flights host ports 80/443 and Docker disk headroom before creating
anything. Check the Profiles guide for per-profile Docker Desktop minimums
(small: 8 CPUs / 8-12 GiB / ~25 GiB free disk; medium and large need
devserver-class resources).`,
	RunE: runUp,
}

type namedPlatformResource struct {
	ns   string
	name string
}

// setOverridesNotice reminds users that --set overrides are ephemeral.
// Returns "" when no overrides were passed.
func setOverridesNotice() string {
	if len(configOverrides) == 0 {
		return ""
	}
	return fmt.Sprintf("Note: --set overrides are ephemeral and are not saved to %q; edit the file to persist them.", loadedConfigPath)
}

// platformDeploymentsForTier lists the deployments a cluster's tier is expected
// to have.
//
// A workload-tier cluster runs none of them: Dex, Headlamp and the
// observability backends are the platform tier, and they live in the hub. The
// list has to match what the cluster actually installs, or the wait never
// completes — which is what waiting for "dex/dex" on a spoke does.
func platformDeploymentsForTier(profile config.ProfileSpec, tier config.ClusterTier) []namedPlatformResource {
	if tier == config.TierWorkload {
		// A spoke has no platform tier, but it does have its own Headlamp and
		// its own policy engine: admission control on the hub cannot admit a
		// pod on a spoke.
		return []namedPlatformResource{
			{"headlamp", "headlamp"},
			{"kyverno", "kyverno-admission-controller"},
			{"observability", "kube-state-metrics"},
			{"observability", "alloy-agent"},
		}
	}
	deployments := []namedPlatformResource{
		{"dex", "dex"},
		{"headlamp", "headlamp"},
		{"observability", "victoria-metrics-k8s-stack-grafana"},
		// The operator-created VMSingle deployment is the metrics backend. If
		// it cannot start (for example, an invalid VMSingle flag), the rest of
		// the platform can look healthy while the public metrics endpoint is
		// dead; wait for it explicitly.
		{"observability", "vmsingle-victoria-metrics-k8s-stack"},
	}
	if profile.PolicyReporter {
		deployments = append(deployments, namedPlatformResource{"policy-reporter", "policy-reporter-ui"})
	}
	return deployments
}

// gatewayRoutesForTier lists the gateway routes a cluster's tier is expected to
// have. The spoke serves its own workloads through its own gateway, not the
// platform's routes.
func gatewayRoutesForTier(profile config.ProfileSpec, tier config.ClusterTier) []namedPlatformResource {
	if tier == config.TierWorkload {
		return []namedPlatformResource{{"headlamp", "headlamp"}}
	}
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
	if currentConfig != nil && targetCluster.ProviderOrDefault() == config.ProviderExisting {
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
