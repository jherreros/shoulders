# WebApplications

WebApplications deploy containerized HTTP services. They are **namespace-scoped**.

```yaml
apiVersion: shoulders.io/v1alpha1
kind: WebApplication
metadata:
  name: team-a-instance
  namespace: team-a
spec:
  image: nginx
  tag: latest
  replicas: 2
  host: my-app.example.com
  port: 8080
```

Key fields: `image`, `tag`, `replicas`, `host`, `port`, `service.port`,
`route.enabled` (default `true`), `env`/`envFrom`, `volumes`/`volumeMounts`,
`readinessProbe`/`livenessProbe`/`startupProbe`, `resources`,
`podSecurityContext`/`securityContext`.

This provisions:

- A Kubernetes **Deployment** with the specified image and replicas.
- A **Service** on `service.port` targeting the container `port`.
- An **HTTPRoute** (Gateway API) bound to the `cilium-gateway` when `route.enabled`
  is true and `host` is set.
- A Cilium ingress policy for Gateway traffic to public WebApplications.

CLI:

```bash
shoulders app init <name> --image <img>
shoulders app update <name>
shoulders app apply -f app.yaml
shoulders app build-image <img> [ctx]
shoulders app load-image <img>
shoulders app list
shoulders app describe <name>
shoulders app delete <name>
```

## Images

vind node architecture follows your machine (arm64 on Apple Silicon).
amd64-only images fail with `exec format error` — prefer
`shoulders app build-image` + `load-image` over upstream prebuilts, and avoid
amd64 digests or amd64-only bases. In zsh loops always brace the tag
(`"${img}:local"`), and check ghcr.io reachability early if upstream
Dockerfiles pull stages from there. Full notes:
[Migrating existing apps](../guides/migrating-apps.md).
