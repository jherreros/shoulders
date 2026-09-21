package airgap

import (
	"fmt"
	"path/filepath"

	"helm.sh/helm/v4/pkg/action"
	helmcli "helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/registry"
)

func helmSettings() *helmcli.EnvSettings {
	return helmcli.New()
}

// PushChartsToRegistry pushes every vendored chart tarball to
// oci://registryAddr/charts (plainHTTP for local throwaway registries).
func PushChartsToRegistry(chartsDir string, meta *BundleMeta, registryAddr string, plainHTTP bool, log func(string)) error {
	regClient, err := registry.NewClient(registry.ClientOptPlainHTTP())
	if err != nil {
		return fmt.Errorf("create registry client: %w", err)
	}
	if !plainHTTP {
		var err error
		regClient, err = registry.NewClient()
		if err != nil {
			return fmt.Errorf("create registry client: %w", err)
		}
	}
	push := action.NewPushWithOpts(
		action.WithPlainHTTP(plainHTTP),
		action.WithPushConfig(&action.Configuration{RegistryClient: regClient}),
	)
	push.Settings = helmSettings()
	for _, chart := range meta.Charts {
		if chart.File == "" {
			return fmt.Errorf("chart %s has no vendored file", chart.Chart)
		}
		archive := filepath.Join(chartsDir, chart.File)
		remote := fmt.Sprintf("oci://%s/%s", registryAddr, chartsOCIPath)
		log(fmt.Sprintf("pushing chart %s", chart.File))
		if _, err := push.Run(archive, remote); err != nil {
			return fmt.Errorf("push chart %s: %w", chart.File, err)
		}
	}
	return nil
}
