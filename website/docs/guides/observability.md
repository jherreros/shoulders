# Observability

Shoulders ships a pre-configured LGTM stack with VictoriaMetrics for metrics:

- **Grafana** — visualization and dashboards.
- **VictoriaMetrics** (victoria-metrics-k8s-stack) — metrics storage, collection, and alerting. The hub runs a single-node `VMSingle`; vmagent scrapes and vmalert evaluates rules.
- **Loki** — log aggregation.
- **Tempo** — distributed tracing.
- **Alloy** — unified collector for logs, metrics and traces.

```bash
shoulders logs <app-name>   # Loki if available, else pod logs
shoulders dashboard         # prefers gateway host, falls back to port-forward
```

The public metrics endpoint is still `prometheus.<domain>`. It now points at
VMSingle's Prometheus-compatible API, so existing queries, Grafana datasources,
and remote-write clients keep the same URL and `/api/v1/write` path.

`small` uses VictoriaMetrics' minimum 24h retention and omits the log/trace
pipeline. Its 2Gi PVC keeps 1.5GiB free, giving roughly a 512MiB data budget
before ingestion pauses. `medium` uses 24h on a 5Gi PVC (about 1GiB of data);
`large` uses 7d on a 20Gi PVC (about 5GiB). These are budgets enforced by
VictoriaMetrics' free-space floor, not hard byte caps.

In a topology, spoke telemetry is push-only. Each spoke runs:

- `kube-state-metrics` for its object state;
- `node-exporter` for node CPU, memory, filesystem, and network;
- a metrics-only `alloy-agent` that scrapes its own apiserver, kubelet
  (`/metrics`, `/metrics/cadvisor`, `/metrics/probes`), CoreDNS, node-exporter,
  and kube-state-metrics, then remote-writes to the hub's VMSingle.

Every spoke series carries `cluster=<name>`. Hub-local series carry no cluster
label, so an empty label means the hub. The hub never scrapes into a spoke for
telemetry.
