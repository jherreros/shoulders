package config

// ProfileSpec describes what a profile includes.
//
// Note that Cilium's observability (the hubble UI, the Cilium dashboards) is
// deliberately absent: it is expressed in git, in
// 2-addons/profiles/<profile>/helm-releases/cilium.yaml, because the CLI
// cannot set it — flux merges the manifest's inline values over the values the
// CLI publishes, so a value set here would be silently overridden. See
// docs/multi-cluster-target.md R5.
type ProfileSpec struct {
	Name              string
	EventStreams      bool
	PolicyReporter    bool
	Trivy             bool
	Falco             bool
	LogTracePipeline  bool
	PostgresInstances int
	KafkaReplicas     int
	KafkaMinISR       int
	KafkaStorage      string
	// MetricsRetention is published as SHOULDERS_METRICS_RETENTION.
	// The public host is still prometheus.<domain>, but the backend is
	// VictoriaMetrics.
	MetricsRetention string
	// MetricsMinFreeDisk is published as SHOULDERS_METRICS_MIN_FREE_DISK. It is
	// the free-space floor passed to VMSingle's -storage.minFreeDiskSpaceBytes;
	// together with the PVC size it bounds how much data the volume can hold.
	MetricsMinFreeDisk string
}

func (cfg *Config) ProfileSpec() ProfileSpec {
	return ProfileSpecFor(cfg.Profile())
}

func ProfileSpecFor(profile string) ProfileSpec {
	switch normalizeProfile(profile) {
	case ProfileSmall:
		return ProfileSpec{
			Name:               ProfileSmall,
			PostgresInstances:  1,
			KafkaReplicas:      1,
			KafkaMinISR:        1,
			KafkaStorage:       "1Gi",
			MetricsRetention:   "24h",
			MetricsMinFreeDisk: "1536MiB",
		}
	case ProfileLarge:
		return ProfileSpec{
			Name:               ProfileLarge,
			EventStreams:       true,
			PolicyReporter:     true,
			Trivy:              true,
			Falco:              true,
			LogTracePipeline:   true,
			PostgresInstances:  2,
			KafkaReplicas:      3,
			KafkaMinISR:        2,
			KafkaStorage:       "1Gi",
			MetricsRetention:   "7d",
			MetricsMinFreeDisk: "15GiB",
		}
	default:
		return ProfileSpec{
			Name:               ProfileMedium,
			EventStreams:       true,
			PolicyReporter:     true,
			Trivy:              true,
			Falco:              true,
			LogTracePipeline:   true,
			PostgresInstances:  2,
			KafkaReplicas:      3,
			KafkaMinISR:        2,
			KafkaStorage:       "1Gi",
			MetricsRetention:   "24h",
			MetricsMinFreeDisk: "4GiB",
		}
	}
}
