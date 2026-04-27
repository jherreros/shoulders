# Shoulders CLI Reference

## Cluster Lifecycle

```bash
shoulders up                              # Create cluster and install platform
shoulders up --name <name>                # Use a specific cluster name
shoulders down                            # Delete the cluster
shoulders start                           # Resume a stopped cluster
shoulders stop                            # Stop cluster without deleting
shoulders status                          # Show platform health
shoulders status --wait                   # Poll until healthy
shoulders cluster list                    # List clusters
shoulders cluster use <name>              # Switch context
shoulders update                          # Self-update the CLI
```

## Workspace Management

Workspaces are isolated team environments. They are **cluster-scoped** (no namespace needed).

```bash
shoulders workspace create <name>         # Create workspace
shoulders workspace use <name>            # Set as active (used as default namespace)
shoulders workspace list                  # List all workspaces
shoulders workspace current               # Show active workspace
shoulders workspace delete <name>         # Delete workspace
```

A workspace creates:
- A Kubernetes namespace named after the workspace
- A default-deny network policy (allows only intra-workspace + system traffic)
- A Kyverno policy enforcing workspace-prefixed resource names

## Web Applications

Deploy containerized HTTP services. **Namespace-scoped** — uses the active workspace by default.

```bash
shoulders app init <name> --image <image> [flags]   # Deploy app
shoulders app list                                   # List apps
shoulders app describe <name>                        # Show full details
shoulders app delete <name>                          # Delete app
```

**Flags for `app init`:**

| Flag | Default | Description |
|------|---------|-------------|
| `--image` | *(required)* | Container image (e.g., `nginx`, `nginx:1.26`) |
| `--tag` | parsed from image | Image tag override |
| `--host` | `<name>.local` | Hostname for HTTP routing |
| `--replicas` | `1` | Number of pod replicas |
| `--port` | `80` | Container port |
| `-n` | active workspace | Target namespace |
| `--dry-run` | `false` | Print YAML without applying |

An app creates:
- A Kubernetes Deployment with the specified image
- A Service on port 80
- An HTTPRoute (Gateway API) routing traffic for the hostname

## Infrastructure: Databases, Caches & Object Buckets

Provision PostgreSQL, Redis, and Garage S3 buckets. **Namespace-scoped.**

```bash
shoulders infra add-db <name> [flags]     # Create StateStore
shoulders infra add-bucket <name> [flags] # Create Garage S3 bucket StateStore
shoulders infra list                      # List all infra
shoulders infra delete <name>             # Delete infra resource
```

**Flags for `add-db`:**

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | `postgres` | `postgres` or `redis` |
| `--tier` | `dev` | `dev` (1Gi storage) or `prod` (10Gi storage) |
| `-n` | active workspace | Target namespace |

A PostgreSQL StateStore creates:
- A CloudNativePG cluster (2 instances)
- An `app-secret` Secret with database credentials

A Redis StateStore creates:
- A Redis Deployment and Service

**Flags for `add-bucket`:**

| Flag | Default | Description |
|------|---------|-------------|
| `--bucket` | resource name | Garage bucket name |
| `--secret` | `<bucket>-s3` | Secret that receives S3 credentials |
| `--read` | `true` | Grant read access |
| `--write` | `true` | Grant write access |
| `--owner` | `false` | Grant owner access |
| `-n` | active workspace | Target namespace |

A bucket StateStore creates:
- A Garage bucket
- A Garage access key bound to the bucket
- A workspace Secret with `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_DEFAULT_REGION`, `AWS_ENDPOINT_URL`, and `S3_BUCKET`

## Infrastructure: Event Streams (Kafka)

Provision Kafka clusters and topics. **Namespace-scoped.**

```bash
shoulders infra add-stream <name> --topics <list> [flags]   # Create EventStream
```

**Flags for `add-stream`:**

| Flag | Default | Description |
|------|---------|-------------|
| `--topics` | *(required)* | Comma-separated topic names (e.g., `logs,events`) |
| `--partitions` | cluster default | Partitions per topic |
| `--replicas` | cluster default | Replication factor |
| `--config` | — | Repeatable `key=value` Kafka topic config |
| `-n` | active workspace | Target namespace |

An EventStream creates:
- A Strimzi KafkaNodePool (3 broker+controller nodes)
- A Strimzi Kafka cluster (KRaft mode, plain + TLS listeners)
- A KafkaTopic for each topic in the list

## Observability

```bash
shoulders logs <app-name>                 # Fetch logs (Loki → pod fallback)
shoulders logs <app-name> -n <namespace>  # Logs from specific namespace
shoulders dashboard                       # Open Grafana
shoulders portal                          # Open Headlamp developer portal
shoulders reporter                        # Open Policy Reporter UI
```

`shoulders logs` queries Loki first for centralized log aggregation. If Loki is unavailable, it falls back to direct pod log streaming.

## Output Formats

Most list and status commands support `-o table|json|yaml`:

```bash
shoulders status -o json
shoulders workspace list -o yaml
shoulders app list -o json
```
