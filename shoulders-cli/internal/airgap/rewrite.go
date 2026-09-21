package airgap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// chartsOCIPath is the repository path under the bundle registry holding
// pushed charts: oci://<host>/<chartsOCIPath>/<chart>:<version>.
const chartsOCIPath = "charts"

// generatedChartsFile is injected into the staged manifests alongside the
// Flux-managed HelmRepository objects.
const generatedChartsFile = "2-addons/manifests/helm-repositories/bundle-oci-charts.yaml"

// ExtractBundle unpacks bundle.tar.gz into a temp dir and returns its path.
// Callers remove it when done.
func ExtractBundle(bundlePath string) (string, error) {
	if _, err := os.Stat(bundlePath); err != nil {
		return "", fmt.Errorf("bundle not found at %s: %w", bundlePath, err)
	}
	extractDir, err := os.MkdirTemp("", "shoulders-bundle-*")
	if err != nil {
		return "", err
	}
	cmd := exec.Command("tar", "-xzf", bundlePath, "-C", extractDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(extractDir) //nolint:errcheck // best-effort temp cleanup
		return "", fmt.Errorf("extract bundle: %w\n%s", err, out)
	}
	return extractDir, nil
}

// StageManifests unpacks the bundled manifests snapshot to a temp staging
// dir and rewrites it for offline install: every bundle-backed HelmRelease
// is repointed at the registry's OCI charts, the generated HelmRepository
// (type oci) objects are added, and the headlamp plugin sidecar (npm/ArtifactHub
// egress at runtime) is disabled. Returns the staged 2-addons directory.
func StageManifests(meta *BundleMeta, extractDir, chartsHost string, insecure bool) (string, func(), error) {
	cleanup := func() {}
	stageRoot, err := os.MkdirTemp("", "shoulders-stage-*")
	if err != nil {
		return "", cleanup, err
	}
	cleanup = func() {
		os.RemoveAll(stageRoot) //nolint:errcheck // best-effort temp cleanup
	}
	// The bundle carries the manifests as a nested manifests.tar.gz file;
	// extract it first (it yields a 2-addons/ tree).
	manifestsTar := filepath.Join(extractDir, BundleManifestsTar)
	if _, err := os.Stat(manifestsTar); err != nil {
		return "", cleanup, fmt.Errorf("bundle manifests archive not found at %s: %w", manifestsTar, err)
	}
	if out, err := exec.Command("tar", "-xzf", manifestsTar, "-C", stageRoot).CombinedOutput(); err != nil {
		return "", cleanup, fmt.Errorf("extract bundle manifests: %w\n%s", err, out)
	}
	staged := filepath.Join(stageRoot, "2-addons")
	if info, err := os.Stat(staged); err != nil || !info.IsDir() {
		return "", cleanup, fmt.Errorf("stage manifests: expected 2-addons tree at %s", staged)
	}

	rewrites := indexRewrites(meta)
	releaseFiles, err := mapReleasesToFiles(filepath.Join(staged, "manifests", "helm-releases"))
	if err != nil {
		return "", cleanup, err
	}
	for _, rewrite := range rewrites {
		file, ok := releaseFiles[rewrite.Release]
		if !ok {
			return "", cleanup, fmt.Errorf("release %s not found in staged manifests", rewrite.Release)
		}
		if err := rewriteReleaseChart(file, rewrite); err != nil {
			return "", cleanup, err
		}
	}
	if err := writeOCIChartSources(filepath.Join(stageRoot, generatedChartsFile), meta, chartsHost, insecure); err != nil {
		return "", cleanup, err
	}
	// The helm-repositories Kustomization lists resources explicitly, so the
	// generated file must be registered or Flux silently ignores it (chart
	// source not found at install time).
	if err := ensureKustomizationResource(filepath.Join(staged, "manifests", "helm-repositories", "kustomization.yaml"), "bundle-oci-charts.yaml"); err != nil {
		return "", cleanup, err
	}
	if err := disableHeadlampPlugins(filepath.Join(staged, "manifests", "helm-releases", "headlamp.yaml")); err != nil {
		return "", cleanup, err
	}
	return staged, cleanup, nil
}

// ensureKustomizationResource appends resource to a Kustomization's
// resources list unless already present. No-op when the file has no
// resources key.
func ensureKustomizationResource(path, resource string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	raw, ok := parsed["resources"]
	if !ok {
		return nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("%s has non-list resources", path)
	}
	for _, item := range items {
		if name, _ := item.(string); name == resource {
			return nil
		}
	}
	parsed["resources"] = append(items, resource)
	updated, err := yaml.Marshal(parsed)
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0o644)
}

func indexRewrites(meta *BundleMeta) []RewriteEntry {
	entries := meta.RewriteEntries()
	for i := range entries {
		for _, chart := range meta.Charts {
			if chart.Release == entries[i].Release {
				entries[i].Version = chart.Version
				entries[i].Chart = chart.Chart
			}
		}
	}
	return entries
}

// mapReleasesToFiles maps HelmRelease metadata.name to the manifest file
// holding it (base releases only; profile overlays patch but never add).
func mapReleasesToFiles(dir string) (map[string]string, error) {
	mapping := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read staged releases: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		docs, err := splitYAMLDocs(path)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			var parsed struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			if err := yaml.Unmarshal(doc, &parsed); err != nil {
				return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
			}
			if parsed.Kind == "HelmRelease" && parsed.Metadata.Name != "" {
				mapping[parsed.Metadata.Name] = path
			}
		}
	}
	return mapping, nil
}

// rewriteReleaseChart repoints one HelmRelease's chart.spec at the bundle
// OCI chart. Charts are served through a HelmRepository of type oci (the
// HelmRelease chart sourceRef API has no OCIRepository kind). Chart name and
// version are set from the vendored entry: a no-op for registry releases,
// and required for git-backed releases (garage) whose chart is a repository
// path (./script/helm/garage) that is meaningless outside a GitRepository.
func rewriteReleaseChart(path string, rewrite RewriteEntry) error {
	docs, err := splitYAMLDocs(path)
	if err != nil {
		return err
	}
	changed := false
	for i, doc := range docs {
		var parsed map[string]interface{}
		if err := yaml.Unmarshal(doc, &parsed); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if parsed["kind"] != "HelmRelease" {
			continue
		}
		metadata, _ := parsed["metadata"].(map[string]interface{})
		if metadata["name"] != rewrite.Release {
			continue
		}
		spec, _ := parsed["spec"].(map[string]interface{})
		chart, _ := spec["chart"].(map[string]interface{})
		chartSpec, _ := chart["spec"].(map[string]interface{})
		if chartSpec == nil {
			return fmt.Errorf("release %s has no chart.spec", rewrite.Release)
		}
		chartSpec["sourceRef"] = map[string]interface{}{
			"kind":      "HelmRepository",
			"name":      rewrite.OCIRepo,
			"namespace": "flux-system",
		}
		chartSpec["chart"] = rewrite.Chart
		chartSpec["version"] = rewrite.Version
		changed = true
		updated, err := yaml.Marshal(parsed)
		if err != nil {
			return err
		}
		docs[i] = updated
	}
	if !changed {
		return fmt.Errorf("release %s not found in %s", rewrite.Release, path)
	}
	return writeSplitDocs(path, docs)
}

// writeOCIChartSources generates one HelmRepository of type oci per
// vendored chart. The repository URL is the shared charts prefix; the chart
// name and version stay in each HelmRelease, and the controller resolves
// oci://<host>/charts/<chart>:<version> (the layout helm push produces).
func writeOCIChartSources(path string, meta *BundleMeta, chartsHost string, insecure bool) error {
	chartsHost = strings.TrimSpace(chartsHost)
	if chartsHost == "" {
		return fmt.Errorf("charts registry host is required")
	}
	docs := make([][]byte, 0, len(meta.Charts))
	for _, chart := range meta.Charts {
		if chart.Release == "" || chart.OCIRepo == "" {
			continue
		}
		obj := map[string]interface{}{
			"apiVersion": "source.toolkit.fluxcd.io/v1",
			"kind":       "HelmRepository",
			"metadata": map[string]interface{}{
				"name":      chart.OCIRepo,
				"namespace": "flux-system",
			},
			"spec": map[string]interface{}{
				"interval": "1m",
				"type":     "oci",
				"url":      fmt.Sprintf("oci://%s/%s", chartsHost, chartsOCIPath),
			},
		}
		if insecure {
			obj["spec"].(map[string]interface{})["insecure"] = true
		}
		content, err := yaml.Marshal(obj)
		if err != nil {
			return err
		}
		docs = append(docs, content)
	}
	return writeSplitDocs(path, docs)
}

// disableHeadlampPlugins turns off the plugin sidecar, which needs
// npm/ArtifactHub egress at every pod start. Airgap divergence, documented.
func disableHeadlampPlugins(path string) error {
	docs, err := splitYAMLDocs(path)
	if err != nil {
		// Headlamp is optional in small profiles only via values; the file
		// always exists in the base set, so a missing file is an error.
		return err
	}
	changed := false
	for i, doc := range docs {
		var parsed map[string]interface{}
		if err := yaml.Unmarshal(doc, &parsed); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if parsed["kind"] != "HelmRelease" {
			continue
		}
		spec, _ := parsed["spec"].(map[string]interface{})
		values, _ := spec["values"].(map[string]interface{})
		if values == nil {
			continue
		}
		plugins, _ := values["pluginsManager"].(map[string]interface{})
		if plugins == nil {
			continue
		}
		plugins["enabled"] = false
		changed = true
		updated, err := yaml.Marshal(parsed)
		if err != nil {
			return err
		}
		docs[i] = updated
	}
	if !changed {
		return fmt.Errorf("headlamp pluginsManager not found in %s", path)
	}
	return writeSplitDocs(path, docs)
}

func writeSplitDocs(path string, docs [][]byte) error {
	var builder strings.Builder
	for i, doc := range docs {
		if i > 0 {
			builder.WriteString("---\n")
		}
		builder.Write(bytesTrimSpace(doc))
		builder.WriteString("\n")
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func bytesTrimSpace(doc []byte) []byte {
	return []byte(strings.TrimSpace(string(doc)))
}
