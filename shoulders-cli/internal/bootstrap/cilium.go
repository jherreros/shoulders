package bootstrap

import (
	"log"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	helmcli "helm.sh/helm/v3/pkg/cli"
)

const (
	ciliumRepoURL   = "https://helm.cilium.io/"
	ciliumChartName = "cilium"
	ciliumVersion   = "1.19.1"
)

func EnsureCilium(kubeconfigPath string) error {
	settings := helmcli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}
	settings.SetNamespace("kube-system")

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), "secret", log.Printf); err != nil {
		return err
	}

	chartPath, err := locateChart(settings)
	if err != nil {
		return err
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return err
	}

	values := map[string]interface{}{
		"kubeProxyReplacement": true,
		"image": map[string]interface{}{
			"pullPolicy": "IfNotPresent",
		},
		"ipam": map[string]interface{}{
			"mode": "kubernetes",
		},
	}

	if releaseExists(actionConfig, ciliumChartName) {
		upgrade := action.NewUpgrade(actionConfig)
		upgrade.Namespace = settings.Namespace()
		upgrade.Version = ciliumVersion
		_, err = upgrade.Run(ciliumChartName, chart, values)
		return err
	}

	install := action.NewInstall(actionConfig)
	install.ReleaseName = ciliumChartName
	install.Namespace = settings.Namespace()
	install.CreateNamespace = true
	install.Version = ciliumVersion
	_, err = install.Run(chart, values)
	return err
}

func locateChart(settings *helmcli.EnvSettings) (string, error) {
	chartPathOptions := &action.ChartPathOptions{
		RepoURL: ciliumRepoURL,
		Version: ciliumVersion,
	}
	return chartPathOptions.LocateChart(ciliumChartName, settings)
}

func releaseExists(cfg *action.Configuration, name string) bool {
	get := action.NewGet(cfg)
	_, err := get.Run(name)
	return err == nil
}
