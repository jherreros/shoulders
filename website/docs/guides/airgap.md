# Airgap installs with bundles

Two-stage flow. Online, against a healthy cluster, vendor everything:

```bash
shoulders vendor -o shoulders-bundle.tar.gz
```

This resolves all Helm charts (union across profiles), harvests every container image
and packs them with addon manifests and the Flux install manifest.

Offline, install from the bundle (vind only):

```bash
shoulders up --bundle shoulders-bundle.tar.gz
```

Known limitations (documented divergences, not bugs):

- Headlamp `pluginsManager` is disabled offline (plugin sidecar needs npm/ArtifactHub).
- Trivy vulnerability databases degrade gracefully offline.
- `provider: existing` + `--bundle` is rejected.
