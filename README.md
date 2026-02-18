# Shoulders

Shoulders is a reference implementation of an Internal Developer Platform (IDP) that demonstrates how to use Crossplane to provide a self-service platform for developers to create and manage their cloud-native applications and infrastructure on Kubernetes.

The name originates from the quote _"If I have seen further it is by standing on the shoulders of Giants"_ by Isaac Newton. The Shoulders platform is composed of a set of open-source tools that work together to provide a platform for running cloud-native applications on Kubernetes. Those applications will, then, run on the shoulders of the maintainers and contributors of all those open-source tools.

## What is Shoulders?

Shoulders allows developers to declaratively provision and manage:

- **Workspaces** — Isolated tenant environments with network policies and naming conventions.
- **Web Applications** — Containerized applications with Deployments, Services, and Gateway API routing.
- **State Stores** — PostgreSQL databases (via CloudNativePG) and Redis caches with independent toggles.
- **Event Streams** — Full Kafka clusters and multiple topics (via Strimzi).
- **Observability** — Built-in LGTM stack (Loki, Grafana, Tempo, Prometheus) for comprehensive monitoring.

All resources are defined as Crossplane Composite Resources and managed through Flux GitOps, a CLI, a developer portal, or an MCP server.

## Architecture

Shoulders follows a multi-layered approach:

### 1. Cluster Layer (`1-cluster/`)
Creates a three-node Kubernetes cluster using **kind** (Kubernetes in Docker) for local development with the default CNI disabled so Cilium can take over.

### 2. Addons Layer (`2-addons/`)
Installs platform components using **FluxCD** for GitOps-based deployment. Flux Kustomizations enforce install order: helm repositories → namespaces → helm releases → crossplane → gateway → headlamp.

- **Helm Repositories & Releases** — Core platform software (Cilium, Crossplane, CloudNativePG, Strimzi, Kyverno, Prometheus stack, Loki, Tempo, Alloy, Headlamp).
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
| [Crossplane](https://crossplane.io) | Composable infrastructure for custom abstractions (XRDs + Compositions) |
| [FluxCD](https://fluxcd.io) | GitOps continuous delivery for Kubernetes |
| [Cilium](https://cilium.io) | CNI with kube-proxy replacement, network policies, and Gateway API implementation |
| [Gateway API](https://gateway-api.sigs.k8s.io) | Kubernetes-native routing (HTTPRoute) backed by Cilium |
| [Strimzi](https://strimzi.io) | Kubernetes-native Apache Kafka operator |
| [CloudNativePG](https://cloudnative-pg.io) | PostgreSQL operator for Kubernetes |
| [Kyverno](https://kyverno.io) | Policy-as-code for security and governance |
| [Headlamp](https://headlamp.dev) | Kubernetes web UI, extended via the Shoulders portal plugin |

## Quick Start

### Install the CLI

```bash
curl -fsSL https://raw.githubusercontent.com/jherreros/shoulders/main/scripts/install.sh | bash
```

### Deploy the platform

```bash
shoulders up
```

This will:
1. Create a local kind cluster named `shoulders`.
2. Install Cilium CNI with kube-proxy replacement and Gateway API support.
3. Bootstrap FluxCD.
4. Deploy all platform components via GitOps and wait for reconciliation.

### Verify installation

```bash
shoulders status
```

## CLI Reference

The `shoulders` CLI supports the following commands:

```
shoulders up                            # Create cluster and install platform
shoulders down                          # Delete the kind cluster
shoulders status                        # Cluster and platform health

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
shoulders infra add-stream <name>       # Create an EventStream (--topics, --partitions, --replicas, --config)
shoulders infra list                    # List StateStores and EventStreams
shoulders infra delete <name>           # Delete an infrastructure resource

shoulders cluster list                  # List local kind clusters
shoulders cluster use <name>            # Switch context to a cluster

shoulders logs <app-name>               # Fetch logs (Loki if available, else pod logs)
shoulders dashboard                     # Open Grafana (port-forward to localhost:3000)
shoulders headlamp                      # Open Headlamp (port-forward to localhost:4466)
```

Global flags: `--kubeconfig`, `--output table|json|yaml`. Most namespace-scoped commands accept `-n <namespace>` or use the active workspace.

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

StateStores provision database and caching services. They are **namespace-scoped**. Both PostgreSQL and Redis can be independently enabled or disabled.

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
```

| Field | Type | Default | Description |
|---|---|---|---|
| `postgresql.enabled` | boolean | `true` | Enable PostgreSQL |
| `postgresql.storage` | string | `1Gi` | PVC storage size |
| `postgresql.databases` | string[] | — | Additional databases to create |
| `redis.enabled` | boolean | `true` | Enable Redis |
| `redis.replicas` | integer | `1` | Redis replicas |

When PostgreSQL is enabled, this creates:
- A CloudNativePG **Cluster** (2 instances) with an `app` user, an `app-secret` Secret (base64 credentials), and any extra databases listed.

When Redis is enabled, this creates:
- A Redis **Deployment** and **Service** (`<name>-redis`).

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

### Accessing Grafana

```bash
shoulders dashboard
```

This port-forwards to `localhost:3000`, prints the Grafana credentials, and opens the browser. The admin password can also be retrieved manually:

```bash
kubectl get secret -n observability kube-prometheus-stack-grafana -o jsonpath='{.data.admin-password}' | base64 -d
```

## Developer Portal (Headlamp Plugin)

The developer portal is delivered as the **Shoulders Headlamp plugin** (`shoulders-portal-plugin/`). When the platform is installed, Headlamp loads the plugin through its `pluginsManager` and exposes it in the sidebar under **Shoulders** at `/shoulders`.

```bash
shoulders headlamp
```

This command prints a login token, starts a port-forward, and opens Headlamp at `http://localhost:4466/shoulders`.

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
│   ├── create-cluster.sh          # Kind cluster setup script
│   └── kind-config.yaml           # Kind configuration (3 nodes, no default CNI)
├── 2-addons/                      # Platform components (GitOps-managed)
│   ├── flux/                      # FluxCD bootstrap (GitRepository + Kustomizations)
│   ├── install-addons.sh          # Addon installation script (Cilium + Flux)
│   └── manifests/
│       ├── crossplane/            # XRDs, Compositions, Functions, RBAC
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

## Contributing

1. Fork the repository.
2. Create a feature branch.
3. Test your changes with a fresh cluster (`shoulders down && shoulders up`).
4. Submit a pull request.

## Cleanup

```bash
shoulders down
```

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
