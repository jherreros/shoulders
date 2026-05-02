<!-- markdownlint-disable MD013 -->
# Shoulders

[![CI](https://github.com/jherreros/shoulders/actions/workflows/test.yml/badge.svg)](https://github.com/jherreros/shoulders/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/jherreros/shoulders?label=release)](https://github.com/jherreros/shoulders/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/jherreros/shoulders?filename=shoulders-cli%2Fgo.mod&label=go)](shoulders-cli/go.mod)
[![License](https://img.shields.io/github/license/jherreros/shoulders)](LICENSE)

Shoulders is a reference implementation of an Internal Developer Platform (IDP) that demonstrates how to use Crossplane to provide a self-service platform for developers to create and manage their cloud-native applications and infrastructure on Kubernetes.

The name originates from the quote _"If I have seen further it is by standing on the shoulders of Giants"_ by Isaac Newton. The Shoulders platform is composed of a set of open-source tools that work together to provide a platform for running cloud-native applications on Kubernetes. Those applications will, then, run on the shoulders of the maintainers and contributors of all those open-source tools.

## What is Shoulders?

Shoulders allows developers to declaratively provision and manage:

- **Workspaces** — Isolated tenant environments with network policies and naming conventions.
- **Web Applications** — Containerized applications with Deployments, Services, and Gateway API routing.
- **State Stores** — PostgreSQL databases (via CloudNativePG), Redis caches, and Garage S3 buckets with independent toggles.
- **Event Streams** — Full Kafka clusters and multiple topics (via Strimzi).
- **Observability** — Built-in LGTM stack (Loki, Grafana, Tempo, Prometheus) for comprehensive monitoring.

All resources are defined as Crossplane Composite Resources and managed through Flux GitOps, a CLI, a developer portal, or an MCP server.

## Architecture

Shoulders follows a multi-layered approach:

### 1. Cluster Layer (`1-cluster/`)
Creates a Kubernetes cluster using **vind** (vCluster in Docker) for local development. Vind provides automatic LoadBalancer support, sleep/wake capability, and pull-through image caching.

### 2. Addons Layer (`2-addons/`)
Installs platform components using **FluxCD** for GitOps-based deployment. Flux Kustomizations enforce install order: helm repositories → namespaces → helm releases → crossplane → gateway → dex → headlamp.

- **Helm Repositories & Releases** — Core platform software (Cilium, Crossplane, CloudNativePG, Strimzi, Kyverno, Prometheus stack, Loki, Tempo, Alloy, Dex, Headlamp, Trivy Operator, Falco, Policy Reporter).
- **Crossplane Abstractions** — XRDs, Compositions, and Functions that define the developer-facing API.
- **Gateway** — Gateway API CRDs and a Cilium-backed `Gateway` resource for HTTP routing.
- **Headlamp** — Developer portal with the Shoulders plugin loaded via `pluginsManager`.

### 3. User Space (`3-user-space/`)
Developer-facing resources where teams provision their applications and infrastructure using high-level abstractions. The `team-a/` directory contains canonical examples.

### 4. CLI (`shoulders-cli/`)
A Go CLI (`shoulders`) for bootstrapping the cluster, managing workspaces and applications, querying logs, and opening dashboards.

### 5. MCP Server (`shoulders-mcp-server/`)
A Model Context Protocol server that exposes the same operations to AI assistants and MCP-compatible clients, talking directly to the Kubernetes API and integrating with Loki/Tempo for observability.

### 6. Developer Portal (`shoulders-portal-plugin/`)
A Headlamp plugin that renders a self-service UI for Shoulders resources inside the cluster dashboard.

## Key Technologies

| Technology | Role |
|---|---|
| [vind](https://github.com/loft-sh/vind) | vCluster in Docker — local Kubernetes clusters with sleep/wake and LoadBalancer support |
| [Crossplane](https://crossplane.io) | Composable infrastructure for custom abstractions (XRDs + Compositions) |
| [FluxCD](https://fluxcd.io) | GitOps continuous delivery for Kubernetes |
| [Cilium](https://cilium.io) | CNI with kube-proxy replacement, network policies, and Gateway API implementation |
| [Gateway API](https://gateway-api.sigs.k8s.io) | Kubernetes-native routing (HTTPRoute) backed by Cilium |
| [Strimzi](https://strimzi.io) | Kubernetes-native Apache Kafka operator |
| [CloudNativePG](https://cloudnative-pg.io) | PostgreSQL operator for Kubernetes |
| [Garage](https://garagehq.deuxfleurs.fr/) | Lightweight S3-compatible object storage |
| [Kyverno](https://kyverno.io) | Policy-as-code for security and governance |
| [Trivy Operator](https://aquasecurity.github.io/trivy-operator/) | Vulnerability and misconfiguration scanning |
| [Falco](https://falco.org) | Runtime threat detection |
| [Policy Reporter](https://kyverno.github.io/policy-reporter/) | Aggregated policy and security reporting UI with Grafana dashboards |
| [Dex](https://dexidp.io) | OIDC identity provider for SSO into Grafana and Headlamp |
| [Headlamp](https://headlamp.dev) | Kubernetes web UI, extended via the Shoulders portal plugin |

## Quick Start

### Install the CLI

With Homebrew:

```bash
brew install jherreros/tap/shoulders
```

Or via the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/jherreros/shoulders/main/scripts/install.sh | bash
```

### Deploy the platform

```bash
shoulders up
```

Note: Shoulders maps vind container ports `80` and `443` to your host to enable local routing for Dex, Grafana, and Headlamp. Ensure those host ports are free before running `shoulders up`.

This will:
1. Create a local vind cluster named `shoulders`.
2. Install Cilium CNI with kube-proxy replacement and Gateway API support.
3. Bootstrap FluxCD.
4. Deploy all platform components via GitOps and wait for reconciliation.

### Deploy onto an existing cluster

Generate an example config, edit it for your target kubeconfig/context, and then run `up` against that file:

```bash
shoulders init --provider existing --config ./shoulders.yaml
shoulders --config ./shoulders.yaml up
```

When `cluster.provider: existing`, `shoulders up` installs the platform onto the selected cluster instead of creating a vind cluster.

### Verify installation

```bash
shoulders status
```

### Stop and resume

You can stop the cluster to free resources and start it again later without re-provisioning:

```bash
shoulders stop            # Stops Docker containers (control-plane + workers)
shoulders start           # Resumes a previously stopped cluster
```

`shoulders start` and `shoulders stop` are local vind-only workflows.

## Configuration

The CLI reads `~/.shoulders/config.yaml` by default. Use `--config <path>` to point at a different file, and repeatable `--set key=value` flags to override file values or run from defaults without a YAML file.

Generate a starter file with:

```bash
shoulders init --provider vind
shoulders init --provider existing --config ./shoulders.yaml
```

Or configure specific parameters:

```bash
shoulders --set cluster.provider=existing --set cluster.context=kind-dev up
shoulders --config ./shoulders.yaml --set platform.profile=small up
```

Current config fields:

```yaml
current_workspace: ""

cluster:
  provider: vind          # vind | existing
  name: shoulders
  kubeconfig: ""
  context: ""

platform:
  profile: medium        # small | medium | large
  cilium:
    enabled: true
    version: "1.19.2"
  flux:
    gitRepository:
      url: "https://github.com/jherreros/shoulders.git"
      branch: "main"
    pathPrefix: "."
```

Notes:
- `provider: vind` preserves the current default behavior.
- `provider: existing` skips cluster creation and targets the configured kube context.
- `platform.profile` selects the addon footprint. `medium` is the default. `small` is laptop-friendly and suitable for a small cluster; it keeps the core IDP, basic Grafana/Prometheus, Dex, Headlamp, Crossplane, Kyverno admission, CNPG, and Garage, but omits Event Streams, Loki/Tempo/Alloy, Hubble UI, Trivy, Falco, and Policy Reporter. `large` keeps the full feature set with a larger local vind topology and longer Prometheus retention.
- Profile overlays live under `2-addons/profiles/` and are valid Flux/Kustomize paths. Non-CLI installs can apply `kubectl apply -k 2-addons/profiles/<profile>/flux` to reconcile the same profile paths from this repo.
- The addon install script also honors `SHOULDERS_PROFILE=small|medium|large`; for example, `SHOULDERS_PROFILE=small 2-addons/install-addons.sh` applies `2-addons/profiles/small/flux`.
- Cilium defaults to enabled for `vind` and disabled for `existing`.
- `platform.flux.gitRepository.url`, `branch`, and `pathPrefix` let you point Flux at a different repository, branch, or subdirectory, as long as that source contains the expected Shoulders manifests under the configured path.
- `--set` supports `current_workspace`, `cluster.provider`, `cluster.name`, `cluster.kubeconfig`, `cluster.context`, `platform.profile`, `platform.domain`, `platform.cilium.enabled`, `platform.cilium.version`, `platform.flux.gitRepository.url`, `platform.flux.gitRepository.branch`, and `platform.flux.pathPrefix`.
- On `provider: existing`, `shoulders down` removes the Flux-managed Shoulders platform from the current cluster. If `platform.cilium.enabled: true`, it also uninstalls the `cilium` Helm release from `kube-system`.

Profile summary:

| Profile | Intended target | Local vind topology | Includes Event Streams | Optional security/reporting |
|---|---|---|---|---|
| `small` | Laptops and small clusters | control plane + 1 worker | No | Kyverno admission only |
| `medium` | Default local platform | control plane + 2 workers | Yes | Trivy, Falco, Policy Reporter |
| `large` | Full local platform with more headroom | control plane + 3 workers | Yes | Trivy, Falco, Policy Reporter |

## CLI Reference

The `shoulders` CLI supports the following commands:

```
shoulders init                          # Write an example config file
shoulders up                            # Create cluster and install platform
shoulders down                          # Delete the vind cluster or uninstall Shoulders from an existing cluster
shoulders start                         # Start a previously stopped vind cluster
shoulders stop                          # Stop the local vind cluster without deleting it
shoulders status                        # Cluster and platform health (nodes, pods, Flux, Crossplane, Gateway)

shoulders workspace create <name>       # Create a Workspace
shoulders workspace list                # List Workspaces
shoulders workspace use <name>          # Set the active workspace
shoulders workspace current             # Show the active workspace
shoulders workspace delete <name>       # Delete a Workspace

shoulders app init <name> --image <img> # Deploy a WebApplication
shoulders app list                      # List WebApplications
shoulders app describe <name>           # Show WebApplication details
shoulders app delete <name>             # Delete a WebApplication

shoulders infra add-db <name>           # Create a StateStore (--type postgres|redis, --tier dev|prod)
shoulders infra add-bucket <name>       # Create a Garage S3 bucket StateStore (--bucket, --secret)
shoulders infra add-stream <name>       # Create an EventStream (--topics, --partitions, --replicas, --topic-config)
shoulders infra list                    # List StateStores and EventStreams
shoulders infra delete <name>           # Delete an infrastructure resource

shoulders cluster list                  # List local vind clusters or kube contexts
shoulders cluster use <name>            # Switch context to a cluster or kube context

shoulders logs <app-name>               # Fetch logs (Loki if available, else pod logs)
shoulders dashboard                     # Open Grafana (prefers the configured gateway host; defaults to grafana.localhost)
shoulders portal                        # Open Headlamp portal (prefers the configured gateway host; defaults to headlamp.localhost)
shoulders reporter                      # Open Policy Reporter UI (prefers the configured gateway host; defaults to reporter.localhost)
```

Global flags: `--config`, `--set key=value`, `--kubeconfig`, `--output table|json|yaml`. Most namespace-scoped commands accept `-n <namespace>` or use the active workspace.

## Platform Abstractions

### Workspace

Workspaces provide isolated environments for teams. They are **cluster-scoped**.

```yaml
apiVersion: shoulders.io/v1alpha1
kind: Workspace
metadata:
  name: team-a
spec: {}
```

This creates:
- A dedicated **Namespace** named after the workspace.
- A default-deny **CiliumNetworkPolicy** allowing only intra-workspace, kube-system, and cnpg-system traffic.
- A **Kyverno ClusterPolicy** enforcing that all workload names are prefixed with the workspace name (e.g. `team-a-*`).

### WebApplication

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
```

| Field | Type | Required | Description |
|---|---|---|---|
| `image` | string | yes | Container image |
| `tag` | string | yes | Image tag |
| `replicas` | integer | yes | Number of pod replicas |
| `host` | string | yes | Hostname for Gateway API routing |

This provisions:
- A Kubernetes **Deployment** with the specified image and replicas.
- A **Service** on port 80.
- An **HTTPRoute** (Gateway API) bound to the `cilium-gateway` in `kube-system`, routing traffic for the given hostname to the service.

### StateStore

StateStores provision database, cache, and object storage services. They are **namespace-scoped**. PostgreSQL, Redis, and object storage can be independently enabled or disabled.

```yaml
apiVersion: shoulders.io/v1alpha1
kind: StateStore
metadata:
  name: team-a-db
  namespace: team-a
spec:
  postgresql:
    databases:
      - team-a-01
      - team-a-02
  redis:
    enabled: true
    replicas: 1
  objectStorage:
    enabled: true
    buckets:
      - name: team-a-assets
        secretName: team-a-assets-s3
        read: true
        write: true
```

| Field | Type | Default | Description |
|---|---|---|---|
| `postgresql.enabled` | boolean | `true` | Enable PostgreSQL |
| `postgresql.storage` | string | `1Gi` | PVC storage size |
| `postgresql.databases` | string[] | — | Additional databases to create |
| `redis.enabled` | boolean | `true` | Enable Redis |
| `redis.replicas` | integer | `1` | Redis replicas |
| `objectStorage.enabled` | boolean | `false` | Enable Garage bucket provisioning |
| `objectStorage.endpoint` | string | `http://garage.garage.svc.cluster.local:3900` | S3 endpoint stored in generated Secrets |
| `objectStorage.region` | string | `garage` | S3 region stored in generated Secrets |
| `objectStorage.buckets[].name` | string | — | Garage bucket name |
| `objectStorage.buckets[].secretName` | string | `<bucket>-s3` | Kubernetes Secret for S3 credentials |
| `objectStorage.buckets[].read` | boolean | `true` | Grant read access to the generated key |
| `objectStorage.buckets[].write` | boolean | `true` | Grant write access to the generated key |
| `objectStorage.buckets[].owner` | boolean | `false` | Grant owner access to the generated key |

When PostgreSQL is enabled, this creates:
- A CloudNativePG **Cluster** (2 instances) with an `app` user, an `app-secret` Secret (base64 credentials), and any extra databases listed.

When Redis is enabled, this creates:
- A Redis **Deployment** and **Service** (`<name>-redis`).

When object storage is enabled, this creates:
- A Garage bucket for each `objectStorage.buckets[]` entry.
- A Garage access key with the requested bucket permissions.
- A Kubernetes **Secret** in the workspace namespace containing `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_DEFAULT_REGION`, `AWS_ENDPOINT_URL`, and `S3_BUCKET`.

### EventStream

EventStreams provision Kafka clusters and topics. They are **namespace-scoped**.

```yaml
apiVersion: shoulders.io/v1alpha1
kind: EventStream
metadata:
  name: team-a-01
  namespace: team-a
spec:
  topics:
    - name: logs
    - name: events
      partitions: 5
      config:
        retention.ms: "604800000"
```

| Field | Type | Default | Description |
|---|---|---|---|
| `topics[].name` | string | — | Topic name (required) |
| `topics[].partitions` | integer | `3` | Number of partitions |
| `topics[].replicas` | integer | `3` | Replication factor |
| `topics[].config` | object | — | Arbitrary Kafka topic configuration |

This provisions:
- A Strimzi **KafkaNodePool** (`<name>-pool`) with 3 broker+controller nodes.
- A Strimzi **Kafka** cluster (`<name>-cluster`) in KRaft mode with plain and TLS listeners.
- A **KafkaTopic** for each entry in the `topics` array.

## Observability

Shoulders comes with a pre-configured observability stack:

- **[Grafana](https://grafana.com/oss/grafana/)** — Visualization and dashboards.
- **[Prometheus](https://prometheus.io)** (via kube-prometheus-stack) — Metrics collection and alerting.
- **[Loki](https://grafana.com/oss/loki/)** — Log aggregation.
- **[Tempo](https://grafana.com/oss/tempo/)** — Distributed tracing.
- **[Grafana Alloy](https://grafana.com/oss/alloy/)** — Unified collector for logs, metrics, and traces.

Use `platform.profile: small` for a low-footprint install: Prometheus retention is capped, the log/trace pipeline is omitted, and scanner/reporting add-ons are not installed. `medium` and `large` include the complete observability and reporting stack.

## Security & Compliance

Shoulders ships with a security and compliance stack that integrates with both Policy Reporter and Grafana:

- **[Kyverno](https://kyverno.io)** — Policy engine with baseline Pod Security Standard policies in audit mode. Generates PolicyReport CRDs consumed by Policy Reporter.
- **[Kyverno Policies](https://kyverno.io/policies/)** — Community best-practice policies (baseline pod security).
- **[Trivy Operator](https://aquasecurity.github.io/trivy-operator/)** — Continuous vulnerability and misconfiguration scanning. Produces VulnerabilityReports integrated into Policy Reporter via its Trivy plugin.
- **[Falco](https://falco.org)** — Runtime threat detection using eBPF. Falcosidekick forwards events as PolicyReport CRDs for Policy Reporter aggregation.
- **[Policy Reporter](https://kyverno.github.io/policy-reporter/)** — Unified dashboard aggregating policy results from Kyverno, Trivy, and Falco. Includes Prometheus metrics and Grafana dashboards.

Access the Policy Reporter UI:

```bash
shoulders reporter
```

## Identity and Access

Shoulders ships with **Dex** as the OIDC identity provider. Grafana and Headlamp are configured to authenticate via Dex.

Dex is exposed over HTTPS at `https://dex.127.0.0.1.sslip.io` by default, or at `https://dex.<platform.domain>` when you set `platform.domain` in the CLI config. The default local issuer uses the repository's development CA from `1-cluster/authentication-config.yaml`; custom domains use a CLI-generated development certificate, so browsers and other host-side tooling may need to accept the warning for that hostname.

Default sample users:

- `admin@example.com` / `password`
- `developer@example.com` / `password`
- `viewer@example.com` / `password`

### Accessing Grafana

```bash
shoulders dashboard
```

This command first tries the configured Grafana gateway host (OIDC via Dex), which defaults to `http://grafana.localhost`, and opens it in your browser. If that host is not reachable, it falls back to local port-forward mode at `http://localhost:3000` and prints Grafana admin credentials.

If you changed cluster networking settings, recreate the cluster for host-port mappings to take effect:

```bash
shoulders down
shoulders up
```

If needed, the Grafana admin password can also be retrieved manually:

```bash
kubectl get secret -n observability kube-prometheus-stack-grafana -o jsonpath='{.data.admin-password}' | base64 -d
```

## Developer Portal (Headlamp Plugin)

The developer portal is delivered as the **Shoulders Headlamp plugin** (`shoulders-portal-plugin/`). When the platform is installed, Headlamp loads the plugin through its `pluginsManager` and exposes it in the sidebar under **Shoulders** at `/shoulders`.

```bash
shoulders portal
```

This command first tries the configured Headlamp gateway host (OIDC via Dex), which defaults to `http://headlamp.localhost`, and opens it in your browser. If that host is not reachable, it falls back to local port-forward mode at `http://localhost:4466`.

### Local plugin development

```bash
cd shoulders-portal-plugin
npm install
npm run start
```

### In-cluster plugin installation

Plugin artifacts are published to Artifact Hub and consumed via the Headlamp `pluginsManager` configuration in `2-addons/manifests/helm-releases/headlamp.yaml`.

## MCP Server

Shoulders includes an MCP server (`shoulders-mcp-server/`) for AI assistants and other MCP-compatible clients. It talks directly to the Kubernetes API for workspace, application, and infrastructure lifecycle operations, and integrates with Loki and Tempo for logs and traces.

Example client configuration:

```json
{
  "mcpServers": {
    "shoulders": {
      "command": "npx",
      "args": ["github:jherreros/shoulders-mcp-server"]
    }
  }
}
```

Available tools include workspace and app CRUD, infrastructure provisioning, platform status, cluster management, log retrieval via Loki, and trace lookup via Tempo. The server also exposes Crossplane schemas and example manifests as MCP resources.

See [shoulders-mcp-server/README.md](shoulders-mcp-server/README.md) for the full tool list and configuration options.

## Project Structure

```
shoulders/
├── 1-cluster/                     # Cluster creation
│   ├── create-cluster.sh          # vind cluster setup script
│   └── vind-config.yaml           # vCluster Docker driver configuration
├── 2-addons/                      # Platform components (GitOps-managed)
│   ├── flux/                      # FluxCD bootstrap (GitRepository + Kustomizations)
│   ├── install-addons.sh          # Addon installation script (Cilium + Flux)
│   └── manifests/
│       ├── crossplane/            # XRDs, Compositions, Functions, RBAC
│       ├── dex/                    # Dex and HTTPRoutes for OIDC host routing
│       ├── gateway/               # Gateway API CRDs + Cilium Gateway
│       ├── headlamp/              # Headlamp RBAC
│       ├── helm-releases/         # Helm chart deployments
│       ├── helm-repositories/     # Helm repository configs
│       └── namespaces/            # Namespace definitions
├── 3-user-space/                  # Developer workspace
│   └── team-a/                    # Example team workspace
│       ├── workspace.yaml         # Workspace definition
│       ├── webapp.yaml            # WebApplication example
│       ├── state-store.yaml       # StateStore example
│       └── event-stream.yaml      # EventStream example
├── shoulders-cli/                 # Go CLI (shoulders)
│   ├── cmd/                       # Cobra commands
│   ├── internal/                  # Bootstrap, Flux, Kube, Crossplane helpers
│   └── pkg/api/                   # Shoulders API types (v1alpha1)
├── shoulders-mcp-server/          # MCP server (TypeScript)
│   ├── src/                       # Server implementation
│   └── tests/                     # Unit tests
├── shoulders-portal-plugin/       # Headlamp plugin (React/TypeScript)
│   └── src/                       # Plugin components and API helpers
├── artifacthub/                   # Artifact Hub metadata for the portal plugin
└── scripts/
    └── install.sh                 # CLI installer script
```

## AI Agent Skill

Shoulders ships with an agent skill that teaches AI assistants how to deploy and manage applications on the platform.

Install globally (works across all workspaces):

```bash
shoulders skill install
```

Or install into the current workspace:

```bash
shoulders skill install --workspace
```

Once installed, agents can invoke the skill via `/shoulders` in VS Code Copilot chat.

## Sub-projects

| Project | Description |
|---------|-------------|
| [shoulders-cli](shoulders-cli/) | Go CLI for cluster bootstrap, workspace and app management |
| [shoulders-mcp-server](shoulders-mcp-server/) | MCP server for AI assistants and MCP-compatible clients |
| [shoulders-portal-plugin](shoulders-portal-plugin/) | Headlamp plugin for a self-service developer portal |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, build instructions, and guidelines.

## Cleanup

```bash
shoulders down
```

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
