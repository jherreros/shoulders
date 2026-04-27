---
name: shoulders
description: "Deploy and manage applications on the Shoulders Internal Developer Platform. USE FOR: deploying web applications, creating workspaces, provisioning PostgreSQL databases, Redis caches, Garage S3 buckets, Kafka event streams, checking platform status, fetching application logs, troubleshooting deployments on Kubernetes. TOOLS: shoulders CLI (primary), shoulders MCP server (optional)."
---

# Shoulders Platform Skill

Deploy and manage cloud-native applications on the Shoulders Internal Developer Platform using the `shoulders` CLI.

## Prerequisites

Before deploying, ensure:

1. **CLI installed**: Run `shoulders --version`. If not installed: `brew install jherreros/tap/shoulders`
2. **Skill installed**: If this skill was not auto-loaded, install it with `shoulders skill install`
3. **Cluster running**: Run `shoulders status`. If no cluster exists: `shoulders up`
4. **Wait for readiness**: If you just ran `shoulders up`, run `shoulders status --wait` to confirm all components are healthy.

## Core Workflow

The standard deployment workflow follows this order:

```
1. Create a workspace    → shoulders workspace create <name>
2. Set active workspace  → shoulders workspace use <name>
3. Deploy application    → shoulders app init <name> --image <image>
4. Add infrastructure    → shoulders infra add-db <name> / add-bucket <name> / add-stream <name>
5. Verify                → shoulders status / shoulders logs <app-name>
```

## Important: Naming Convention

**All resources in a workspace must be prefixed with the workspace name.** This is enforced by Kyverno policy. For example, in workspace `team-a`:

- Application names: `team-a-api`, `team-a-frontend`
- Database names: `team-a-db`
- Stream names: `team-a-events`

Failing to prefix will result in the resource being rejected.

## Quick-Start Example

```bash
shoulders workspace create demo
shoulders workspace use demo
shoulders app init demo-api --image nginx:latest --host demo.local
shoulders infra add-db demo-db --type postgres --tier dev
shoulders infra add-bucket demo-assets --bucket demo-assets
shoulders logs demo-api
```

For more deployment patterns (multi-service, full-stack with Kafka, etc.), see [examples](./references/examples.md).

## Key Commands

| Task | Command |
|------|---------|
| Create workspace | `shoulders workspace create <name>` |
| Set active workspace | `shoulders workspace use <name>` |
| Deploy app | `shoulders app init <name> --image <img> [--host h] [--port p] [--replicas n]` |
| Add PostgreSQL | `shoulders infra add-db <name> --type postgres [--tier dev\|prod]` |
| Add Redis | `shoulders infra add-db <name> --type redis` |
| Add S3 bucket | `shoulders infra add-bucket <name> [--bucket bucket-name] [--secret secret-name]` |
| Add Kafka topics | `shoulders infra add-stream <name> --topics "t1,t2" [--partitions n] [--config k=v]` |
| List resources | `shoulders app list` / `shoulders infra list` / `shoulders workspace list` |
| View logs | `shoulders logs <app-name>` |
| Check status | `shoulders status [--wait]` |
| Dry-run | `shoulders app init <name> --image <img> --dry-run` |
| Delete resource | `shoulders app delete <name>` / `shoulders infra delete <name>` |

For full CLI reference with all flags and details, see [cli-reference](./references/cli-reference.md).

## Troubleshooting

### Platform not healthy

```bash
shoulders status                          # Check what's failing
shoulders status --wait                   # Wait for reconciliation
```

If status shows issues with Flux or Crossplane, the platform may still be bootstrapping. Wait 2-3 minutes and check again.

### Application not working

```bash
shoulders app describe <name>             # Check resource status
shoulders logs <name>                     # Check application logs
```

### Resource name rejected

If you see a policy violation, ensure the resource name is prefixed with the workspace name. In workspace `team-a`, use names like `team-a-myapp`, not `myapp`.

### Cluster context issues

```bash
shoulders cluster list                    # See available clusters
shoulders cluster use <name>              # Switch to correct cluster
```

## MCP Server

If the Shoulders MCP server is available, it can be used as an alternative to CLI commands. See [mcp-server](./references/mcp-server.md) for tool mapping and configuration.
