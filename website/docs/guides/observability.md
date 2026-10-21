# Observability

Shoulders ships a pre-configured LGTM stack:

- **Grafana** — visualization and dashboards.
- **Prometheus** (kube-prometheus-stack) — metrics and alerting.
- **Loki** — log aggregation.
- **Tempo** — distributed tracing.
- **Alloy** — unified collector for logs, metrics and traces.

```bash
shoulders logs <app-name>   # Loki if available, else pod logs
shoulders dashboard         # prefers gateway host, falls back to port-forward
```

`small` caps Prometheus retention and omits the log/trace pipeline. `medium` and
`large` include the complete stack.

In a topology, spoke telemetry is push-only: each spoke runs `kube-state-metrics`
and a metrics-only `alloy-agent` that remote-writes to the hub's Prometheus with
`cluster=<name>`. Hub-local series carry no cluster label, so an empty label
means the hub.
