package airgap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"sigs.k8s.io/yaml"
)

// helmReleasesDir is scanned for the chart closure. Profile overlays only
// remove releases (small) or change values (small/large kps+kyverno), never
// add charts, so the base union covers every profile.
const helmReleasesDir = "2-addons/manifests/helm-releases"

// helmReposDir maps HelmRepository names to URLs.
const helmReposDir = "2-addons/manifests/helm-repositories"

// garageRepoName is the GitRepository-backed chart (sparse checkout).
const garageRepoName = "garage"

// ciliumRepoURL and ciliumChartName describe the CLI-managed Cilium install.
// Its version comes from config (DefaultCiliumVersion), which differs from
// the suspended Flux HelmRelease pin.
const (
	ciliumRepoURL   = "https://helm.cilium.io/"
	ciliumChartName = "cilium"
)

// ChartInventory scans the base HelmRelease manifests plus the CLI-managed
// Cilium chart and returns the deduplicated chart closure for all profiles.
func ChartInventory(repoRoot string) ([]ChartEntry, error) {
	releases, err := parseHelmReleases(filepath.Join(repoRoot, helmReleasesDir))
	if err != nil {
		return nil, err
	}
	repoURLs, garage, err := parseHelmRepos(filepath.Join(repoRoot, helmReposDir))
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	entries := []ChartEntry{}
	add := func(entry ChartEntry) {
		key := entry.Chart + "@" + entry.Version + "@" + entry.Release
		if seen[key] {
			return
		}
		seen[key] = true
		entries = append(entries, entry)
	}
	for _, release := range releases {
		source := release.SourceKind
		switch source {
		case "HelmRepository":
			repoURL, ok := repoURLs[release.SourceName]
			if !ok {
				return nil, fmt.Errorf("helmrelease %s references unknown helmrepository %s", release.Name, release.SourceName)
			}
			add(ChartEntry{
				Release: release.Name,
				Chart:   release.Chart,
				Version: release.Version,
				RepoURL: repoURL,
				OCIRepo: ociRepoName(release.Name),
			})
		case "GitRepository":
			if release.SourceName != garageRepoName {
				return nil, fmt.Errorf("helmrelease %s references unsupported gitrepository %s", release.Name, release.SourceName)
			}
			// Garage chart name/version are read from its Chart.yaml at
			// vendor time; the release mapping is recorded here.
			add(ChartEntry{
				Release: release.Name,
				Chart:   "garage",
				RepoURL: garage.URL,
				Commit:  garage.Commit,
				OCIRepo: ociRepoName(release.Name),
			})
		default:
			return nil, fmt.Errorf("helmrelease %s has unsupported chart source %s", release.Name, source)
		}
	}
	// The CLI installs Cilium directly from its own version pin.
	add(ChartEntry{
		Chart:   ciliumChartName,
		Version: config.DefaultCiliumVersion,
		RepoURL: ciliumRepoURL,
	})
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Chart != entries[j].Chart {
			return entries[i].Chart < entries[j].Chart
		}
		if entries[i].Version != entries[j].Version {
			return entries[i].Version < entries[j].Version
		}
		return entries[i].Release < entries[j].Release
	})
	return entries, nil
}

func ociRepoName(release string) string {
	var builder strings.Builder
	builder.WriteString("chart-")
	for _, r := range strings.ToLower(strings.TrimSpace(release)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

type helmReleaseRef struct {
	Name       string
	Chart      string
	Version    string
	SourceKind string
	SourceName string
	ChartPath  string
}

type gitRepoRef struct {
	URL    string
	Commit string
}

func parseHelmReleases(dir string) ([]helmReleaseRef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read helm releases dir: %w", err)
	}
	refs := []helmReleaseRef{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		docs, err := splitYAMLDocs(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			var parsed struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Spec struct {
					Chart struct {
						Spec struct {
							Chart     string `yaml:"chart"`
							Version   string `yaml:"version"`
							SourceRef struct {
								Kind string `yaml:"kind"`
								Name string `yaml:"name"`
							} `yaml:"sourceRef"`
						} `yaml:"spec"`
					} `yaml:"chart"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(doc, &parsed); err != nil {
				return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
			}
			if parsed.Kind != "HelmRelease" {
				continue
			}
			spec := parsed.Spec.Chart.Spec
			if spec.Chart == "" || spec.SourceRef.Kind == "" {
				continue
			}
			refs = append(refs, helmReleaseRef{
				Name:       parsed.Metadata.Name,
				Chart:      spec.Chart,
				Version:    strings.TrimSpace(spec.Version),
				SourceKind: spec.SourceRef.Kind,
				SourceName: spec.SourceRef.Name,
				ChartPath:  spec.Chart,
			})
		}
	}
	return refs, nil
}

func parseHelmRepos(dir string) (map[string]string, gitRepoRef, error) {
	urls := map[string]string{}
	garage := gitRepoRef{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, garage, fmt.Errorf("read helm repos dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		docs, err := splitYAMLDocs(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, garage, err
		}
		for _, doc := range docs {
			var parsed struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Spec struct {
					URL string `yaml:"url"`
					Ref struct {
						Commit string `yaml:"commit"`
					} `yaml:"ref"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(doc, &parsed); err != nil {
				return nil, garage, fmt.Errorf("parse %s: %w", entry.Name(), err)
			}
			switch parsed.Kind {
			case "HelmRepository":
				urls[parsed.Metadata.Name] = strings.TrimSpace(parsed.Spec.URL)
			case "GitRepository":
				if parsed.Metadata.Name == garageRepoName {
					garage = gitRepoRef{URL: strings.TrimSpace(parsed.Spec.URL), Commit: strings.TrimSpace(parsed.Spec.Ref.Commit)}
				}
			}
		}
	}
	return urls, garage, nil
}

func splitYAMLDocs(path string) ([][]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	parts := strings.Split(string(content), "\n---")
	docs := make([][]byte, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" || trimmed == "---" {
			continue
		}
		docs = append(docs, []byte(trimmed))
	}
	return docs, nil
}
