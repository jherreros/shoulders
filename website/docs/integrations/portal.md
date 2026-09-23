# Developer portal

The developer portal is the **Shoulders Headlamp plugin** (`shoulders-portal-plugin/`).
When installed, Headlamp loads it via `pluginsManager` under **Shoulders** at `/shoulders`.

```bash
shoulders portal
```

Tries the configured Headlamp gateway host first (OIDC via Dex, default
`http://headlamp.localhost`), falling back to `http://localhost:4466` port-forward.

Local plugin development:

```bash
cd shoulders-portal-plugin
npm install
npm run start
```

In-cluster installation consumes ArtifactHub artifacts via
`2-addons/manifests/helm-releases/headlamp.yaml`.
