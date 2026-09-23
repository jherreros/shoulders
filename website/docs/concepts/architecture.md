# Architecture

Shoulders follows a multi-layered approach:

## 1. Cluster Layer (`1-cluster/`)

Creates a Kubernetes cluster using **vind** (vCluster in Docker) for local development.
Vind provides automatic LoadBalancer support, sleep/wake capability and pull-through
image caching.

## 2. Addons Layer (`2-addons/`)

Installs platform components using **FluxCD** for GitOps-based deployment. Flux
Kustomizations enforce install order: helm repositories → namespaces → helm releases →
crossplane → gateway → dex → headlamp.

- **Helm Repositories & Releases** — Cilium, Crossplane, CloudNativePG, Strimzi,
  Kyverno, Prometheus stack, Loki, Tempo, Alloy, Dex, Headlamp, Trivy Operator, Falco,
  Policy Reporter.
- **Crossplane Abstractions** — XRDs, Compositions and Functions defining the
  developer-facing API.
- **Gateway** — Gateway API CRDs and a Cilium-backed `Gateway` resource for HTTP routing.
- **Headlamp** — developer portal with the Shoulders plugin loaded via `pluginsManager`.

## 3. User Space (`3-user-space/`)

Developer-facing resources where teams provision applications and infrastructure using
high-level abstractions. `team-a/` contains canonical examples.

## 4. Interfaces

- **CLI** (`shoulders-cli/`) — bootstrap, workspaces, apps, logs, dashboards.
- **MCP Server** (`shoulders-mcp-server/`) — same operations for AI assistants via MCP.
- **Developer Portal** (`shoulders-portal-plugin/`) — self-service UI inside Headlamp.
