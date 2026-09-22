# Contributing to Shoulders

## Prerequisites

- **Docker** — Required for vind (vCluster in Docker)
- **Go** 1.25+ — CLI development (`shoulders-cli/`)
- **Node.js** 20+ — MCP server and portal plugin
- **kubectl** — Kubernetes interaction

## Repository Structure

| Directory | Language | Description |
|-----------|----------|-------------|
| `1-cluster/` | Shell/YAML | Cluster provisioning (vind) |
| `2-addons/` | YAML | Platform components (FluxCD, Crossplane, Helm) |
| `3-user-space/` | YAML | Example team resources |
| `shoulders-cli/` | Go | CLI tool |
| `shoulders-mcp-server/` | TypeScript | MCP server |
| `shoulders-portal-plugin/` | TypeScript/React | Headlamp plugin |

## Building

### CLI

```bash
cd shoulders-cli
go mod tidy
go generate ./...
go build -o shoulders
```

### MCP Server

```bash
cd shoulders-mcp-server
npm install
npm run build
```

### Portal Plugin

```bash
cd shoulders-portal-plugin
npm install
npm run build
```

## Testing

### CLI

```bash
cd shoulders-cli
go test -v ./...
```

Lint:

```bash
golangci-lint run --timeout 5m
```

### MCP Server

```bash
cd shoulders-mcp-server
npm test
```

### Portal Plugin

```bash
cd shoulders-portal-plugin
npm test
npx headlamp-plugin tsc   # type check
npm run lint
```

### Integration

Test your changes end-to-end with a fresh cluster. While developing,
always use local mode so Flux reconciles your working tree (including
uncommitted changes) instead of requiring a git push:

```bash
shoulders down
shoulders up --local --set platform.profile=small
# ... edit 2-addons/... ...
shoulders sync
shoulders status --wait
```

The standard loop is `up --local` once, then `sync` after every change.
Each sync pushes a new immutable `local-<sha>-<timestamp>[-dirty]` tag to
an automatic in-cluster registry and waits for Flux to reconcile it (pass
`--wait=false` to return immediately). `small` keeps the feedback loop fast;
switch to `medium` when your change touches Event Streams, observability, or
policy components (see cluster sizes below). A plain `shoulders up` (git
source) is only needed to validate the default install path before release.

## Developing the platform (local mode)

`platform.flux.source: git` is the default, but during development set it
aside: `--local` snapshots `2-addons/` from disk, so what you test is
exactly what is on disk — no commit, no push, no waiting on remote
poll intervals. Guidelines:

1. **Prefer `up --local` + `sync` over `up` for every iteration.** The git
   path exists for releases and CI; iterating through pushed branches is
   slower and pollutes history.
2. **Use `small` unless you need the omitted components.** Small keeps the
   core IDP (PostgreSQL, Redis, Garage, Grafana/Prometheus, Dex, Headlamp,
   Crossplane, Kyverno) with one vind worker and converges in minutes.
   Medium adds Kafka (3 brokers), Loki/Tempo/Alloy, Hubble UI, Trivy, Falco,
   and Policy Reporter — much heavier (see resource guidance).
3. **`shoulders status --wait` must be green before opening a PR** that
   touches `2-addons/` or the CLI install path.
4. **Airgap changes need the bundle loop.** If you touch chart inventory,
   image handling, or `StageManifests` rewrites, also run
   `shoulders vendor -o bundle.tar.gz` plus `up --bundle bundle.tar.gz` on a
   `small` cluster: the medium profile does not fit laptop hardware (see
   below), and several bundle bugs only reproduce on a fresh install.
5. **Keep snapshots reproducible.** The snapshot only packages `2-addons/`;
   build output, `.git`, and other trees never leak in. Tags are immutable
   by construction (`latest` is never used).
6. **Watch for `stop`/`start` cycles.** The local registry persists on a
   5Gi volume and `start` re-pushes the persisted tag if storage was wiped;
   CoreDNS forwarding is reconciled on every boot. If Flux reports a source
   pull failure after a restart, `shoulders sync` restores it.

## Cluster sizes and resource requirements

vind clusters share the Docker Desktop VM, so profile weight shows up as
node `DiskPressure`, evictions, and webhook timeouts — not just slowness.
What each profile runs:

| Profile | vind nodes | Kafka | Observability (Loki/Tempo/Alloy, Hubble) | Trivy / Falco / Policy Reporter | Postgres | Prometheus |
|---------|-----------|-------|------------------------------------------|---------------------------------|----------|------------|
| `small` | control-plane + 1 worker | no | no | no | 1 instance | 6h / 512MiB |
| `medium` (default) | control-plane + 2 workers | yes, 3 brokers | yes | yes | 2 instances | 24h / 1GiB |
| `large` | control-plane + 3 workers | yes, 3 brokers | yes | yes | 2 instances | 7d / 5GiB |

Measured data points (Apple Silicon laptop, 11 CPUs, ~18 GiB host RAM,
Docker Desktop VM with 11 CPUs / ~12 GiB RAM):

- `small`: fully healthy — 37/37 pods, Flux/Crossplane/Gateway green.
- `medium`: never converged on that VM — node `DiskPressure` on all nodes,
  mass pod evictions (`ContainerStatusUnknown`/`Evicted`), cascading
  Kyverno webhook timeouts. The platform logic was fine; the hardware
  budget was not.
- `shoulders vendor` produces a ~3 GiB bundle (18 charts, ~60 images at the
  time of writing); budget that plus unpacked image layers per node on top
  of the profile itself.

Guidelines:

- **Laptops: develop on `small`.** Give Docker Desktop at least 8 CPUs,
  8–12 GiB memory, and keep ~25 GiB free host disk (VM disk + images +
  a spare bundle tarball).
- **`medium`: use a devserver or CI runners** with at least 8 CPUs and
  16 GiB Docker memory, ~40 GiB free disk. Do not treat a red `medium`
  `status` on a laptop as a code regression without checking
  `kubectl describe nodes` for `DiskPressure` first.
- **`large`: devservers only** (estimate: 8+ CPUs, 32 GiB Docker memory,
  60+ GiB disk) — it exists for the full addon set with week-long
  Prometheus retention, not for routine development.
- When filing issues about unhealthy platforms, always include
  `shoulders status` output, the profile, Docker Desktop CPU/memory
  settings, and whether nodes report memory/disk pressure.

## Pull Request Guidelines

1. Fork the repository and create a feature branch from `main`.
2. Follow existing code style — the CI enforces `golangci-lint` for Go and ESLint for TypeScript.
3. Add or update tests when changing behavior.
4. Use [conventional commit](https://www.conventionalcommits.org/) messages (e.g., `feat:`, `fix:`, `chore:`).
5. Keep PRs focused — one logical change per PR.

## Crossplane Development

When adding or modifying platform abstractions:

1. Define the XRD in `2-addons/manifests/crossplane/definitions/`.
2. Implement the composition in `2-addons/manifests/crossplane/compositions/`.
3. Add RBAC permissions in `2-addons/manifests/crossplane/rbac/crossplane-composed-resources.yaml`.
4. Add a canonical example in `3-user-space/team-a/`.

See [`.github/copilot-instructions.md`](.github/copilot-instructions.md) for Crossplane composition patterns and conventions.

## AI-Assisted Contributions

This repository includes Copilot workspace instructions (`.github/copilot-instructions.md`) and an agent skill (`.github/skills/shoulders/`) that provide context about the project's architecture and conventions. AI agents working in this codebase will automatically pick up these instructions.

## Releases

Releases are automated via GoReleaser on `v*` tags. The release workflow:

1. Builds CLI binaries for Linux/macOS/Windows (amd64/arm64).
2. Updates the Homebrew formula in `jherreros/homebrew-tap`.
3. Builds and publishes the Headlamp portal plugin.
4. Updates Artifact Hub metadata.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
