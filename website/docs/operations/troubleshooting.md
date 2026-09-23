# Troubleshooting

- Ensure host ports `80`/`443` are free before `shoulders up`.
- `shoulders status` shows nodes, pods, Flux, Crossplane and Gateway health.
- `shoulders logs <app>` uses Loki when available, else pod logs.
- After changing networking settings, `shoulders down && shoulders up`.
- Registry data survives `stop`/`start` (5Gi PV); if lost, `shoulders sync` restores it.

# Cleanup

```bash
shoulders down
```
