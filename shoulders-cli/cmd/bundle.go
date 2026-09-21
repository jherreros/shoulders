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
	"github.com/jherreros/shoulders/shoulders-cli/internal/ocisource"
	"github.com/jherreros/shoulders/shoulders-cli/internal/tui"
)

// prepareBundleFluxSource serves a bundle install: charts go to the local
// registry, staged (rewritten) manifests go up as a snapshot next to them,
// and the vendored Flux install manifest is returned for EnsureFluxWithManifest.
func prepareBundleFluxSource(ctx context.Context, tracker *tui.PhaseTracker, meta *airgap.BundleMeta, bundleDir, profile string) (bootstrap.FluxSource, []byte, error) {
	fail := func(err error) (bootstrap.FluxSource, []byte, error) {
		return bootstrap.FluxSource{}, nil, err
	}
	chartsDir := filepath.Join(bundleDir, airgap.BundleChartsDir)
	tracker.UpdateDetail(verboseDetail("pushing %d vendored charts to the local registry", len(meta.Charts)))
	if err := withRegistryPortForward(ctx, func(pushAddr string) error {
		return airgap.PushChartsToRegistry(chartsDir, meta, pushAddr, true, func(message string) {
			tracker.UpdateDetail(verboseDetail("%s", message))
		})
	}); err != nil {
		return fail(fmt.Errorf("push bundle charts: %w", err))
	}

	tracker.UpdateDetail(verboseDetail("staging bundle manifests for offline install"))
	staged, cleanup, err := airgap.StageManifests(meta, bundleDir, ocisource.RegistryAddr(), true)
	if err != nil {
		return fail(err)
	}
	defer cleanup()
	snapshot, err := ocisource.BuildStagedSnapshot(staged, meta.ShouldersRef, time.Now(), "airgap", nil)
	if err != nil {
		return fail(err)
	}
	tracker.UpdateDetail(verboseDetail("pushing staged snapshot %s", snapshot.Tag))
	if err := withRegistryPortForward(ctx, func(pushAddr string) error {
		pushCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		return ocisource.PushArtifact(pushCtx, pushAddr, ocisource.RegistryRepoPath, snapshot.Tag, snapshot.Payload, true)
	}); err != nil {
		return fail(fmt.Errorf("push staged snapshot: %w", err))
	}

	fluxManifest, err := os.ReadFile(filepath.Join(bundleDir, airgap.BundleFluxFile))
	if err != nil {
		return fail(fmt.Errorf("read vendored flux manifest: %w", err))
	}
	source := bootstrap.FluxSource{
		Kind:        config.FluxSourceOCI,
		OCIURL:      ocisource.PullURL(),
		OCITag:      snapshot.Tag,
		OCIInsecure: true,
		PathPrefix:  ".",
		Profile:     profile,
	}
	return source, fluxManifest, nil
}
