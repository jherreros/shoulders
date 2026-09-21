package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/cli"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/flux"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/ocisource"
	"github.com/spf13/cobra"
)

var (
	syncWait    bool
	syncTimeout time.Duration
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Push the local working tree as an OCI artifact and reconcile Flux",
	Long: `Build an OCI artifact from the local Shoulders working tree (including
uncommitted changes), push it to the configured registry, point Flux at the
new immutable tag, and wait for the Kustomizations to reconcile.

On vind clusters with no ociRepository.url configured, a throwaway in-cluster
registry is created automatically and plain-HTTP pull is enabled. On other
clusters (or when ociRepository.url is set), the artifact is pushed to that
registry directly.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := currentConfig.Profile()
		source, tag, err := prepareLocalOCISource(cmd.Context(), profile)
		if err != nil {
			return err
		}
		fmt.Printf("Pushed snapshot %s to %s\n", tag, source.OCIURL)
		if err := bootstrap.ApplyFluxSourceAndKustomizations(cmd.Context(), kubeconfig, source); err != nil {
			return fmt.Errorf("apply flux source: %w", err)
		}
		if err := requestFluxReconcile(cmd.Context(), "OCIRepository"); err != nil {
			return err
		}
		if syncWait {
			if err := waitForFluxReady(cmd.Context(), syncTimeout); err != nil {
				return err
			}
			fmt.Println("Flux reconciled successfully")
		} else {
			fmt.Println("Reconcile requested; not waiting (--wait=false)")
		}
		if err := saveCurrentConfig(); err != nil {
			return fmt.Errorf("persist OCI source coordinates: %w", err)
		}
		return nil
	},
}

// prepareLocalOCISource snapshots the working tree, pushes it, and records
// the new OCI coordinates in the runtime config (persisted to disk so later
// `up`/`status` runs agree). It returns the FluxSource to apply and the tag.
func prepareLocalOCISource(ctx context.Context, profile string) (bootstrap.FluxSource, string, error) {
	root, err := cli.FindRepoRoot()
	if err != nil {
		return bootstrap.FluxSource{}, "", fmt.Errorf("local sync requires a Shoulders checkout: %w", err)
	}
	snapshot, err := ocisource.BuildSnapshot(root, time.Now())
	if err != nil {
		return bootstrap.FluxSource{}, "", err
	}

	pullURL := currentConfig.FluxOCIURL()
	insecure := currentConfig.FluxOCIInsecure()

	if currentConfig.Provider() == config.ProviderVind && (pullURL == "" || pullURL == ocisource.PullURL()) {
		// Local iteration: throwaway in-cluster registry, push via
		// port-forward, pull via cluster DNS. Matching the well-known
		// local pull URL counts as local mode so repeated syncs keep
		// working after the coordinates are persisted.
		pullURL = ocisource.PullURL()
		insecure = true
		if err := pushToLocalRegistry(ctx, snapshot.Payload, snapshot.Tag); err != nil {
			return bootstrap.FluxSource{}, "", err
		}
	} else if pullURL == "" {
		return bootstrap.FluxSource{}, "", fmt.Errorf("no OCI registry configured: set platform.flux.ociRepository.url (or run on a vind cluster for an automatic local registry)")
	} else {
		addr, repoPath, err := ocisource.SplitOCIURL(pullURL)
		if err != nil {
			return bootstrap.FluxSource{}, "", err
		}
		pushCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := ocisource.PushArtifact(pushCtx, addr, repoPath, snapshot.Tag, snapshot.Payload, insecure); err != nil {
			return bootstrap.FluxSource{}, "", err
		}
	}

	currentConfig.Platform.Flux.Source = config.FluxSourceOCI
	currentConfig.Platform.Flux.OCIRepository.URL = pullURL
	currentConfig.Platform.Flux.OCIRepository.Tag = snapshot.Tag
	currentConfig.Platform.Flux.OCIRepository.Insecure = insecure
	// NOTE: not persisted here. Callers save after the cluster state has
	// converged so a failed run cannot leave half-migrated config behind.
	return bootstrap.FluxSourceFromConfig(currentConfig, profile), snapshot.Tag, nil
}

// pushToLocalRegistry ensures the in-cluster registry, opens a port-forward,
// pushes payload under tag over plain HTTP, and closes the forward on return.
func pushToLocalRegistry(ctx context.Context, payload []byte, tag string) error {
	return withRegistryPortForward(ctx, func(pushAddr string) error {
		pushCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		return ocisource.PushArtifact(pushCtx, pushAddr, ocisource.RegistryRepoPath, tag, payload, true)
	})
}

// withRegistryPortForward ensures the local registry and runs fn with a
// localhost address forwarding to it.
func withRegistryPortForward(ctx context.Context, fn func(pushAddr string) error) error {
	if err := ocisource.EnsureLocalRegistry(ctx, kubeconfig); err != nil {
		return err
	}
	localPort, err := ocisource.FreePort()
	if err != nil {
		return err
	}
	stopCh, _, err := kube.PortForwardService(ctx, kubeconfig, ocisource.RegistryNamespace, ocisource.RegistryName, localPort, ocisource.RegistryPort)
	if err != nil {
		return fmt.Errorf("port-forward local OCI registry: %w", err)
	}
	defer close(stopCh)
	return fn(fmt.Sprintf("127.0.0.1:%d", localPort))
}

// repushLocalSnapshotIfNeeded restores the persisted OCI tag into the local
// registry when the artifact is gone (e.g. stop/start wiped registry
// storage). It is a best-effort hook for `start`: it re-pushes under the
// already-configured tag so no config churn occurs, and warns instead of
// failing when there is nothing to do or no checkout is available.
func repushLocalSnapshotIfNeeded(ctx context.Context) error {
	if currentConfig.FluxSource() != config.FluxSourceOCI || currentConfig.FluxOCIURL() != ocisource.PullURL() {
		return nil
	}
	tag := currentConfig.FluxOCITag()
	if tag == "" {
		return nil
	}
	client, err := kube.NewDynamicClient(kubeconfig)
	if err != nil {
		return err
	}
	sources, err := flux.PendingSources(ctx, client, "flux-system")
	if err != nil {
		return err
	}
	if _, ok := flux.FirstSourcePullFailure(sources); !ok {
		return nil
	}
	root, err := cli.FindRepoRoot()
	if err != nil {
		return fmt.Errorf("local OCI artifact %q is unreachable and no Shoulders checkout was found here; run 'shoulders sync' from a checkout to restore it", tag)
	}
	snapshot, err := ocisource.BuildSnapshot(root, time.Now())
	if err != nil {
		return err
	}
	if err := pushToLocalRegistry(ctx, snapshot.Payload, tag); err != nil {
		return err
	}
	return requestFluxReconcile(ctx, "OCIRepository")
}

// requestFluxReconcile nudges the source and all Kustomizations so the new
// artifact is picked up without waiting for the poll interval. Each patch
// is retried: right after boot the vind API server drops requests
// transiently (EOF / connection reset) while it settles.
func requestFluxReconcile(ctx context.Context, sourceKind string) error {
	client, err := kube.NewDynamicClient(kubeconfig)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := retryReconcileRequest(ctx, func() error {
		return flux.RequestSourceReconcile(ctx, client, "flux-system", sourceKind, "flux-system", now)
	}); err != nil {
		return fmt.Errorf("request source reconcile: %w", err)
	}
	items, err := flux.ListKustomizations(ctx, client, "flux-system")
	if err != nil {
		return fmt.Errorf("list kustomizations: %w", err)
	}
	for _, item := range items {
		name := item.GetName()
		if err := retryReconcileRequest(ctx, func() error {
			return flux.RequestKustomizationReconcile(ctx, client, "flux-system", name, now)
		}); err != nil {
			return fmt.Errorf("request %s reconcile: %w", name, err)
		}
	}
	return nil
}

// retryReconcileRequest runs fn up to 3 times with backoff, returning the
// last error. Reconcile patches are idempotent annotation updates.
func retryReconcileRequest(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 5 * time.Second):
		}
	}
	return err
}

// waitForFluxReady polls Kustomizations and sources until everything is Ready
// or the timeout elapses. Source pull failures and missing-path failures
// abort early with the configured-source hint.
func waitForFluxReady(ctx context.Context, timeout time.Duration) error {
	client, err := kube.NewDynamicClient(kubeconfig)
	if err != nil {
		return err
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("timed out waiting for Flux to reconcile")
		case <-ticker.C:
			if sources, err := flux.PendingSources(deadlineCtx, client, "flux-system"); err == nil {
				if failed, ok := flux.FirstSourcePullFailure(sources); ok {
					return fmt.Errorf("flux source %s/%s cannot be fetched: %s%s", failed.Kind, failed.Name, failed.Message, fluxSourceHint())
				}
			}
			pending, err := flux.PendingKustomizations(deadlineCtx, client, "flux-system")
			if err != nil {
				continue
			}
			if len(pending) == 0 {
				return nil
			}
			if failed, ok := flux.FirstMissingPathFailure(pending); ok {
				return fmt.Errorf("flux kustomization %q cannot find its configured path: %s%s", failed.Name, failed.Message, fluxSourceHint())
			}
			fmt.Printf("Waiting for Flux: %s\n", flux.FormatPending(pending))
		}
	}
}

func init() {
	syncCmd.Flags().BoolVar(&syncWait, "wait", true, "Wait until Flux Kustomizations reconcile the new artifact")
	syncCmd.Flags().DurationVar(&syncTimeout, "timeout", 10*time.Minute, "Maximum time to wait for Flux reconciliation with --wait")
	rootCmd.AddCommand(syncCmd)
}
