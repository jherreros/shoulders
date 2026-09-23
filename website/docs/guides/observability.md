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
