package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
)

const fluxInstallURL = "https://github.com/fluxcd/flux2/releases/download/v2.6.4/install.yaml"

func EnsureFlux(ctx context.Context, kubeconfigPath, repoRoot string) error {
	manifest, err := downloadFluxManifest(ctx)
	if err != nil {
		return err
	}
	if err := kube.ApplyManifest(ctx, kubeconfigPath, manifest, ""); err != nil {
		return fmt.Errorf("apply flux install manifest: %w", err)
	}

	fluxDir := filepath.Join(repoRoot, "2-addons", "flux")
	files := []string{"git-repository.yaml", "kustomizations.yaml"}
	for _, name := range files {
		path := filepath.Join(fluxDir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := kube.ApplyManifest(ctx, kubeconfigPath, content, "flux-system"); err != nil {
			return fmt.Errorf("apply flux config %s: %w", name, err)
		}
	}
	return nil
}

func downloadFluxManifest(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fluxInstallURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to download flux install manifest: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}
