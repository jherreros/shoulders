package airgap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/ocisource"
)

// VendorOptions control bundle creation.
type VendorOptions struct {
	RepoRoot    string
	ClusterName string
	Kubeconfig  string
	OutPath     string
	Log         func(string)
}

// Vendor builds a self-contained airgap bundle: chart tarballs (pulled +
// packaged), container images (harvested + pulled + saved), the Flux install
// manifest, and the addon manifests snapshot.
func Vendor(ctx context.Context, opts VendorOptions) (*BundleMeta, error) {
	log := opts.Log
	if log == nil {
		log = func(string) {}
	}
	workDir, err := os.MkdirTemp("", "shoulders-vendor-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir) //nolint:errcheck // best-effort temp cleanup
	chartsDir := filepath.Join(workDir, BundleChartsDir)
	imagesDir := filepath.Join(workDir, "images")
	if err := os.MkdirAll(chartsDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return nil, err
	}

	sha, _ := ocisource.GitDescribe(opts.RepoRoot)
	meta := &BundleMeta{
		Format:       BundleFormatVersion,
		Created:      time.Now().UTC(),
		ShouldersRef: sha,
		FluxURL:      bootstrap.FluxInstallURL,
	}

	log("resolving chart inventory")
	inventory, err := ChartInventory(opts.RepoRoot)
	if err != nil {
		return nil, err
	}

	log("pulling helm charts")
	if err := pullCharts(ctx, inventory, chartsDir, meta, log); err != nil {
		return nil, err
	}

	log("downloading flux install manifest")
	fluxManifest, err := bootstrap.DownloadFluxManifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("download flux manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, BundleFluxFile), fluxManifest, 0o644); err != nil {
		return nil, err
	}

	log("snapshotting addon manifests")
	snapshot, err := ocisource.BuildSnapshot(opts.RepoRoot, meta.Created)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(workDir, BundleManifestsTar), snapshot.Payload, 0o644); err != nil {
		return nil, err
	}

	log("harvesting container images from the cluster")
	harvested, err := HarvestClusterImages(opts.Kubeconfig)
	if err != nil {
		return nil, err
	}
	clusterName := opts.ClusterName
	if clusterName == "" {
		clusterName = "shoulders"
	}
	nodeImages, err := VindNodeImages(ctx, clusterName)
	if err != nil {
		log(fmt.Sprintf("warning: could not list vind node images: %v", err))
	}
	images := unionStrings(harvested, nodeImages, VendorExtras())
	arch, err := NodeArch(opts.Kubeconfig)
	if err != nil {
		log(fmt.Sprintf("warning: could not determine node architecture, vendoring host platform: %v", err))
	} else {
		log(fmt.Sprintf("vendoring %s images", arch))
	}
	log(fmt.Sprintf("copying %d container images", len(images)))
	if _, err := CopyImages(ctx, images, imagesDir, arch, log); err != nil {
		return nil, err
	}
	meta.Images = make([]ImageEntry, 0, len(images))
	for _, ref := range images {
		meta.Images = append(meta.Images, ImageEntry{Ref: ref, Digest: ImageDigest(ctx, ref)})
	}

	if err := WriteBundleMeta(filepath.Join(workDir, BundleMetaFile), meta); err != nil {
		return nil, err
	}

	log(fmt.Sprintf("assembling %s", opts.OutPath))
	if err := AssembleBundle(workDir, opts.OutPath); err != nil {
		return nil, err
	}
	return meta, nil
}

func unionStrings(sets ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, set := range sets {
		for _, item := range set {
			if !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
	}
	return out
}
