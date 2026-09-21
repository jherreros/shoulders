// Package ocisource snapshots the local Shoulders addon manifests into an OCI
// artifact that Flux reconciles from via an OCIRepository.
//
// Only the 2-addons subtree is packaged: every Flux Kustomization path lives
// under 2-addons/, while the rest of the working tree (portal plugin,
// CLI build output, .git) would bloat the artifact by orders of magnitude.
// Tar entry names keep the 2-addons/ prefix so Kustomization paths
// (./2-addons/...) resolve identically to a GitRepository source.
package ocisource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshot is a gzipped tarball of the addon manifests plus the metadata
// needed to publish it as an immutable OCI artifact tag.
type Snapshot struct {
	Payload []byte
	Files   int
	Root    string
	SHA     string
	Dirty   bool
	Tag     string
}

// AddonsDir is the only subtree packaged into the snapshot.
const AddonsDir = "2-addons"

// BuildSnapshot tars repoRoot/2-addons (including uncommitted and untracked
// files) and derives an immutable artifact tag from the git HEAD, dirtiness,
// and timestamp. The git metadata still comes from the repo root so the tag
// reflects the checkout the snapshot was taken from.
func BuildSnapshot(repoRoot string, now time.Time) (*Snapshot, error) {
	return BuildSnapshotWithOverlay(repoRoot, now, nil)
}

// BuildSnapshotWithOverlay works like BuildSnapshot but overlays extra files
// (repo-root-relative paths like "2-addons/manifests/x.yaml" to content)
// onto the tarball, replacing same-named files from disk or adding new ones.
// Used for airgap installs, where generated chart sources and rewritten
// HelmReleases ride along with the vendored manifests.
func BuildSnapshotWithOverlay(repoRoot string, now time.Time, extra map[string][]byte) (*Snapshot, error) {
	addonsRoot := filepath.Join(repoRoot, AddonsDir)
	if info, err := os.Stat(addonsRoot); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("expected addon manifests at %s", addonsRoot)
	}
	sha, dirty := GitDescribe(repoRoot)
	return buildSnapshot(addonsRoot, sha, dirty, now, "local", extra)
}

// BuildStagedSnapshot snapshots an already-prepared 2-addons directory
// (e.g. airgap rewrites staged on disk) under an explicit tag lineage.
// addonsDir is the 2-addons directory itself; entry names keep the
// 2-addons/ prefix.
func BuildStagedSnapshot(addonsDir, sha string, now time.Time, prefix string, extra map[string][]byte) (*Snapshot, error) {
	if info, err := os.Stat(addonsDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("expected staged addon manifests at %s", addonsDir)
	}
	return buildSnapshot(addonsDir, sha, true, now, prefix, extra)
}

func buildSnapshot(addonsDir, sha string, dirty bool, now time.Time, prefix string, extra map[string][]byte) (*Snapshot, error) {
	payload, files, err := tarGzip(filepath.Dir(addonsDir), addonsDir, extra)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Payload: payload,
		Files:   files,
		Root:    addonsDir,
		SHA:     sha,
		Dirty:   dirty,
		Tag:     SnapshotTag(prefix, sha, dirty, now),
	}, nil
}

// SnapshotTag builds an immutable OCI tag: <prefix>-<sha>-<timestamp>[-dirty].
func SnapshotTag(prefix, sha string, dirty bool, now time.Time) string {
	sha = sanitizeTagSegment(sha)
	if sha == "" {
		sha = "nogit"
	}
	tag := fmt.Sprintf("%s-%s-%s", prefix, sha, now.UTC().Format("20060102150405"))
	if dirty {
		tag += "-dirty"
	}
	return tag
}

// GitDescribe returns the short HEAD SHA and whether the working tree is
// dirty. Best effort: outside a git checkout it returns ("nogit", true) so
// the tag still reflects that the content is untracked.
func GitDescribe(repoRoot string) (sha string, dirty bool) {
	shaOut, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "nogit", true
	}
	sha = strings.TrimSpace(string(shaOut))
	if sha == "" {
		sha = "nogit"
	}
	statusOut, err := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if err != nil {
		return sha, true
	}
	return sha, len(bytes.TrimSpace(statusOut)) > 0
}

// NewTag builds an immutable OCI tag: local-<sha>-<timestamp>[-dirty].
// Timestamps make every snapshot unique so Flux never serves a cached
// artifact for a new tag.
func NewTag(sha string, dirty bool, now time.Time) string {
	return SnapshotTag("local", sha, dirty, now)
}

func sanitizeTagSegment(segment string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(segment) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), ".-")
}

func tarGzip(repoRoot, addonsRoot string, extra map[string][]byte) ([]byte, int, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	// Zero the gzip header timestamp for reproducible output.
	gz.ModTime = time.Time{}
	tarWriter := tar.NewWriter(gz)

	files := 0
	written := map[string]bool{}
	writeFile := func(name string, content []byte, mode fs.FileMode) error {
		if written[name] {
			return fmt.Errorf("duplicate tar entry %q", name)
		}
		written[name] = true
		header := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Size:     int64(len(content)),
			Mode:     int64(mode.Perm()),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write(content); err != nil {
			return err
		}
		files++
		return nil
	}
	writeDir := func(name string) error {
		if written[name] {
			return nil
		}
		written[name] = true
		return tarWriter.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: 0o755})
	}
	err := filepath.WalkDir(addonsRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			if _, ok := extra[name]; ok {
				return nil
			}
			if entry.IsDir() {
				return writeDir(name)
			}
			return nil
		}
		if content, ok := extra[name]; ok {
			return writeFile(name, content, info.Mode())
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFile(name, content, info.Mode())
	})
	if err != nil {
		return nil, 0, fmt.Errorf("archive addon manifests at %s: %w", addonsRoot, err)
	}
	// Overlay files that do not exist on disk, in sorted order for
	// deterministic output. Parent dirs are implied; tar readers cope.
	overlayOnly := make([]string, 0, len(extra))
	for name := range extra {
		if !written[name] {
			overlayOnly = append(overlayOnly, name)
		}
	}
	sort.Strings(overlayOnly)
	for _, name := range overlayOnly {
		if err := writeFile(name, extra[name], 0o644); err != nil {
			return nil, 0, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, 0, err
	}
	if err := gz.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), files, nil
}
