# Troubleshooting

- Ensure host ports `80`/`443` are free before `shoulders up`.
- `shoulders status` shows nodes, pods, Flux, Crossplane and Gateway health.
- `shoulders logs <app>` uses Loki when available, else pod logs.
- After changing networking settings, `shoulders down && shoulders up`.
- Registry data survives `stop`/`start` (5Gi PV); if lost, `shoulders sync` restores it.
- Node images (`app load-image`/`build-image`) also survive `stop`/`start`.
  ImagePullBackOff after a restart means the local copy was pruned and the tag
  can't be re-pulled from a registry — keep local copies until green, and prune
  cautiously during break-glass.

# Cleanup

```bash
shoulders down
```
