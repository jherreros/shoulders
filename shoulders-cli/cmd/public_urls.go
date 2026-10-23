package cmd

import "github.com/jherreros/shoulders/shoulders-cli/internal/config"

// Public URLs must come from the resolved target cluster, not the shared
// config. A topology can put its domain on the hub entry, so using
// currentConfig here would silently fall back to the monocluster defaults.
func selectedHeadlampURL(cluster *config.ResolvedCluster) string {
	return cluster.HeadlampURL()
}

func selectedGrafanaURL(cluster *config.ResolvedCluster) string {
	return cluster.GrafanaURL()
}

func selectedReporterURL(cluster *config.ResolvedCluster) string {
	return cluster.ReporterURL()
}
