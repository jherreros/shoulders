# Migrating existing apps

This page collects what two real migrations (Bank of Anthos, OpenTelemetry Demo)
taught us about moving existing manifests onto Shoulders.

## Service renames (workspace prefix)

Workspaces enforce that workload names start with the workspace name
(`<workspace>-*`). Migrating usually means renaming services
(`ledgerwriter` → `bank-ledgerwriter`), which forces updates everywhere the old
name appears:

- Caller environment variables (`*_API_ADDR`, `*_API_PORT`, connection strings).
- Service ports: Shoulders `service.port` defaults to `80`, so an upstream
  `:8080` becomes `:80` unless you pass `--service-port` / set `service.port`.
  The container port itself is `--port` (`spec.port`).

Search callers for the old hostname before you declare a service migrated.

## Secret mounts and key paths

`--secret-mount secretName:mountPath` mounts the whole secret. If the app
expects different filenames (e.g. `publickey`/`privatekey` vs.
`jwtRS256.key[.pub]`), either remap the app's env to the secret's key names or
use `--secret-items secretName:key=path[,key=path...]` to map individual keys
to file paths.

## Disable cloud tracing locally

Demos built for GCP often default to `ENABLE_TRACING=true`, which crashes
without Application Default Credentials (obscure grpc traceback). For local
runs, set it to `false`:

```bash
shoulders app init <name> --image <img> --env ENABLE_TRACING=false
```

## OpenTelemetry SDKs: disable per SDK, verify each

`OTEL_SDK_DISABLED=true` is not honored uniformly:

- Go, Python, .NET honor it.
- Ruby **crashes** with it set — override per service back to `false`.
- C++, PHP and Java-agent builds ignore it or retry noisily against
  `localhost:4317`, producing log volume (which burns disk).

Verify each service individually after disabling.

## Rebuilding database URIs

Point apps at `<store>-rw.<namespace>.svc.cluster.local`, database
`spec.postgresql.database`, with `username`/`password` from the
`<name>-app-secret` Secret (dev-tier defaults `app` / `password` — change them
outside local development).

PostgreSQL bootstrap SQL belongs in `--init-sql` (repeatable):

```bash
shoulders infra add-db <name> \
  --init-sql "CREATE EXTENSION IF NOT EXISTS pgcrypto;" \
  --init-sql "$(cat schema.sql)"
```

## Migrating Jib-built Java apps

Jib produces no Dockerfile and often pins amd64-only bases
(`eclipse-temurin:17-jre-alpine`). For Shoulders:

1. Add a plain Dockerfile with a multi-arch base
   (`eclipse-temurin:17-jre`, not the alpine variant).
2. Build with `shoulders app build-image` so the image matches the vind node
   architecture.
3. Watch for upstream packaging typos Jib masks (e.g. a wrong `mainClass` that
   only surfaces as `ClassNotFoundException` under `java -jar`).

## Container images and architectures

vind node architecture follows your machine (arm64 on Apple Silicon).
Upstream prebuilt images pinned to amd64 fail with
`exec /bin/sh: exec format error` (`CrashLoopBackOff`). Rule of thumb:

- Prefer `shoulders app build-image <img> [ctx]` + `load-image` over upstream
  prebuilts.
- Avoid amd64 digests and amd64-only base images.
- If a registry (e.g. ghcr.io) is unreachable from your machine, sort that out
  before migrating: several upstream Dockerfiles pull stages from ghcr.

Shell quoting pitfall (zsh): `$img:local` in loops triggers the `:l`
modifier. Always brace it:

```bash
for img in $services; do
  shoulders app build-image "${img}:local" "./src/${img}"
done
```

## From imperative CLI to GitOps

Use the CLI for day-0 scaffolding and iteration, then commit manifests and let
GitOps own them:

```bash
shoulders app init <name> --image <img> --dry-run > app.yaml
shoulders infra add-db <name> --dry-run > statestore.yaml
# review, commit, then:
shoulders app apply -f app.yaml
kubectl apply -f statestore.yaml   # or Flux
```

Verify the conversion is exact: `kubectl diff -f app.yaml` should be empty
after applying. Escape hatch for anything created imperatively:
`kubectl get <kind> <name> -o yaml`, then strip `status`, `resourceVersion`,
`uid` and `crossplane.resourceRefs` before committing.
