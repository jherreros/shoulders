// Package airgap builds and installs self-contained platform bundles for
// environments without internet access.
//
// Two-stage flow: `shoulders vendor` (online) resolves every Helm chart,
// harvests every container image, and packs them with the addon manifests
// and the Flux install manifest into bundle.tar.gz. `shoulders up --bundle`
// (offline) imports images into the cluster nodes, serves charts from a
// registry, and reconciles Flux from a rewritten manifests snapshot that
// references only bundled artifacts.
package airgap

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"sigs.k8s.io/yaml"
)

// BundleFormatVersion is written into every bundle so installs can reject
// bundles from incompatible CLI versions.
const BundleFormatVersion = "v1"

// Layout inside bundle.tar.gz.
const (
	BundleMetaFile     = "bundle.yaml"
	BundleManifestsTar = "manifests.tar.gz"
	BundleFluxFile     = "flux-install.yaml"
	BundleChartsDir    = "charts"
	// BundleImagesDir holds one docker-save archive per image
	// (NN-<sanitized-ref>.tar): containerd's ctr import cannot consume
	// multi-image archives.
	BundleImagesDir = "images"
)

// BundleMeta describes a vendor bundle. Charts and Images are the complete
// closure needed for an offline install of any profile.
type BundleMeta struct {
	Format       string       `yaml:"format" json:"format"`
	Created      time.Time    `yaml:"created" json:"created"`
	ShouldersRef string       `yaml:"shouldersRef" json:"shouldersRef"`
	FluxURL      string       `yaml:"fluxURL" json:"fluxURL"`
	Charts       []ChartEntry `yaml:"charts" json:"charts"`
	Images       []ImageEntry `yaml:"images" json:"images"`
}

// ChartEntry is one vendored Helm chart plus the rewrite coordinates used
// at install time to repoint its HelmRelease at an OCI registry.
type ChartEntry struct {
	// Release is the HelmRelease metadata.name (empty for the CLI-managed
	// cilium chart, which has no HelmRelease in scope at install time).
	Release string `yaml:"release" json:"release"`
	// Chart and Version identify the upstream chart.
	Chart   string `yaml:"chart" json:"chart"`
	Version string `yaml:"version" json:"version"`
	// File is the .tgz name under charts/.
	File string `yaml:"file" json:"file"`
	// RepoURL is where the chart was pulled from (informational).
	RepoURL string `yaml:"repoURL,omitempty" json:"repoURL,omitempty"`
	// OCIRepo is the generated HelmRepository (type oci) object name used at
	// install time (chart-<release>). Empty for the CLI-managed cilium chart.
	OCIRepo string `yaml:"ociRepo,omitempty" json:"ociRepo,omitempty"`
	// Commit pins git-backed charts (garage) to the manifest ref.
	Commit string `yaml:"commit,omitempty" json:"commit,omitempty"`
}

// ImageEntry is one vendored container image with its resolved digest.
type ImageEntry struct {
	Ref    string `yaml:"ref" json:"ref"`
	Digest string `yaml:"digest" json:"digest"`
}

// RewriteEntry tells the installer how to repoint one HelmRelease manifest
// at the bundle registry. Derived from ChartEntry at install time.
type RewriteEntry struct {
	// File is the 2-addons-relative manifest path containing the release.
	File string
	// Release is the HelmRelease metadata.name.
	Release string
	// Chart and Version select the OCI artifact.
	Chart   string
	Version string
	// OCIRepo is the generated HelmRepository (type oci) object name to reference.
	OCIRepo string
}

// RewriteEntries returns the install-time rewrites for all chart-backed
// releases (skips the CLI-managed cilium entry, which has no HelmRelease
// to rewrite — the suspended Cilium HelmRelease keeps its placeholder).
func (m *BundleMeta) RewriteEntries() []RewriteEntry {
	entries := make([]RewriteEntry, 0, len(m.Charts))
	for _, chart := range m.Charts {
		if chart.Release == "" || chart.OCIRepo == "" {
			continue
		}
		entries = append(entries, RewriteEntry{
			Release: chart.Release,
			Chart:   chart.Chart,
			Version: chart.Version,
			OCIRepo: chart.OCIRepo,
		})
	}
	return entries
}

// FindChart returns the vendored chart file for an upstream chart+version.
func (m *BundleMeta) FindChart(chart, version string) (ChartEntry, error) {
	for _, entry := range m.Charts {
		if entry.Chart == chart && entry.Version == version {
			return entry, nil
		}
	}
	return ChartEntry{}, fmt.Errorf("bundle has no chart %s version %s", chart, version)
}

// WriteBundleMeta marshals meta to path.
func WriteBundleMeta(path string, meta *BundleMeta) error {
	content, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// ReadBundleMeta reads bundle.yaml from an extracted bundle directory.
func ReadBundleMeta(extractDir string) (*BundleMeta, error) {
	content, err := os.ReadFile(filepath.Join(extractDir, BundleMetaFile))
	if err != nil {
		return nil, fmt.Errorf("read bundle metadata: %w", err)
	}
	var meta BundleMeta
	if err := yaml.Unmarshal(content, &meta); err != nil {
		return nil, fmt.Errorf("parse bundle metadata: %w", err)
	}
	if meta.Format != BundleFormatVersion {
		return nil, fmt.Errorf("unsupported bundle format %q (want %q)", meta.Format, BundleFormatVersion)
	}
	return &meta, nil
}

// AssembleBundle tars the staged workDir into outPath.
func AssembleBundle(workDir, outPath string) error {
	return tarGzipDir(workDir, outPath)
}

func tarGzipDir(srcDir, outPath string) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck // flushed via gzip close below
	gz := gzip.NewWriter(out)
	tarWriter := tar.NewWriter(gz)

	names := []string{}
	err = filepath.WalkDir(srcDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		info, err := os.Stat(filepath.Join(srcDir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		header.ModTime = time.Time{}
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(filepath.Join(srcDir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if _, err := io.Copy(tarWriter, file); err != nil {
			_ = file.Close()
			return err
		}
		_ = file.Close()
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	return gz.Close()
}
