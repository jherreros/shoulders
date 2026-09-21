package airgap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"helm.sh/helm/v4/pkg/action"

	"sigs.k8s.io/yaml"
)

// pullCharts vendors every chart in the inventory into destDir and records
// file names and garage chart metadata on meta.
func pullCharts(ctx context.Context, inventory []ChartEntry, destDir string, meta *BundleMeta, log func(string)) error {
	settings := helmSettings()
	meta.Charts = make([]ChartEntry, 0, len(inventory))
	for _, entry := range inventory {
		if entry.Release == garageReleaseName() {
			packaged, err := vendorGarageChart(ctx, entry, destDir, log)
			if err != nil {
				return err
			}
			meta.Charts = append(meta.Charts, packaged)
			continue
		}
		pull := action.NewPull(action.WithConfig(&action.Configuration{}))
		pull.RepoURL = entry.RepoURL
		pull.Version = entry.Version
		pull.DestDir = destDir
		pull.Settings = settings
		before, err := tgzFiles(destDir)
		if err != nil {
			return fmt.Errorf("list chart dir: %w", err)
		}
		if _, err := pull.Run(entry.Chart); err != nil {
			return fmt.Errorf("pull chart %s version %s: %w", entry.Chart, entry.Version, err)
		}
		after, err := tgzFiles(destDir)
		if err != nil {
			return fmt.Errorf("list chart dir: %w", err)
		}
		saved, err := newFile(before, after)
		if err != nil {
			return fmt.Errorf("locate pulled %s version %s: %w", entry.Chart, entry.Version, err)
		}
		entry.File = saved
		log(fmt.Sprintf("vendored %s", entry.File))
		meta.Charts = append(meta.Charts, entry)
	}
	return nil
}

// tgzFiles lists .tgz basenames in dir.
func tgzFiles(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tgz") {
			files[entry.Name()] = true
		}
	}
	return files, nil
}

// newFile returns the single .tgz present after pull but not before.
func newFile(before, after map[string]bool) (string, error) {
	candidates := []string{}
	for name := range after {
		if !before[name] {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("expected exactly one new chart archive, found %d", len(candidates))
	}
	return candidates[0], nil
}

func garageReleaseName() string {
	return garageRepoName
}

// vendorGarageChart sparse-clones the garage repo at the pinned commit and
// packages its Helm chart, filling in chart name/version from Chart.yaml.
func vendorGarageChart(ctx context.Context, entry ChartEntry, destDir string, log func(string)) (ChartEntry, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return ChartEntry{}, fmt.Errorf("git not found in PATH (needed to vendor the garage chart)")
	}
	commit, url, err := garageSource(entry)
	if err != nil {
		return ChartEntry{}, err
	}
	cloneDir, err := os.MkdirTemp("", "shoulders-garage-*")
	if err != nil {
		return ChartEntry{}, err
	}
	defer os.RemoveAll(cloneDir) //nolint:errcheck // best-effort temp cleanup
	for _, args := range [][]string{
		{"clone", "--filter=blob:none", "--no-checkout", url, cloneDir},
		{"-C", cloneDir, "sparse-checkout", "set", "script/helm/garage"},
		{"-C", cloneDir, "checkout", commit},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return ChartEntry{}, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	chartDir := filepath.Join(cloneDir, "script", "helm", "garage")
	chartMeta, err := readChartMeta(chartDir)
	if err != nil {
		return ChartEntry{}, err
	}
	pack := action.NewPackage()
	pack.Destination = destDir
	saved, err := pack.Run(chartDir, nil)
	if err != nil {
		return ChartEntry{}, fmt.Errorf("package garage chart: %w", err)
	}
	entry.Chart = chartMeta.Name
	entry.Version = chartMeta.Version
	entry.File = filepath.Base(saved)
	log(fmt.Sprintf("vendored %s (garage %s @ %s)", entry.File, url, commit))
	return entry, nil
}

func garageSource(entry ChartEntry) (commit, url string, err error) {
	// The commit pin comes from the GitRepository manifest so the bundle
	// always matches what Flux reconciles online.
	if entry.RepoURL == "" {
		return "", "", fmt.Errorf("garage entry is missing its git URL")
	}
	if entry.Commit == "" {
		return "", "", fmt.Errorf("garage entry is missing its git commit pin")
	}
	return entry.Commit, entry.RepoURL, nil
}

func readChartMeta(chartDir string) (struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}, error) {
	var chartMeta struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	content, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	if err != nil {
		return chartMeta, fmt.Errorf("read garage Chart.yaml: %w", err)
	}
	if err := yaml.Unmarshal(content, &chartMeta); err != nil {
		return chartMeta, fmt.Errorf("parse garage Chart.yaml: %w", err)
	}
	if chartMeta.Name == "" || chartMeta.Version == "" {
		return chartMeta, fmt.Errorf("garage Chart.yaml is missing name/version")
	}
	return chartMeta, nil
}
