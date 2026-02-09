# Shoulders MCP Server

A Model Context Protocol (MCP) server for the Shoulders Internal Developer Platform. It talks directly to the Kubernetes API for workspace, application, and infrastructure lifecycle operations, and integrates with Loki/Tempo for logs and traces.

## Quick Start

Requires Node.js 18.17+.

Run via npx from the standalone repo (synced from the monorepo):

```bash
npx github:jherreros/shoulders-mcp-server
```

Example MCP client configuration:

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

Build and run locally:

```bash
cd /Users/juan/source/shoulders/shoulders-mcp-server
npm install
npm run build
node dist/server.js
```

## Tools

**Workspaces**
- `create_workspace`: Create a new workspace.
- `list_workspaces`: List all workspaces.
- `use_workspace`: Set the active workspace.
- `current_workspace`: Read the active workspace.
- `delete_workspace`: Delete a workspace.

**Applications**
- `deploy_app`: Create a WebApplication.
- `list_apps`: List WebApplications.
- `get_app_status`: Fetch a WebApplication manifest.
- `delete_app`: Delete a WebApplication.

**Infrastructure**
- `add_database`: Create a StateStore.
- `add_stream`: Create an EventStream.
- `list_infra`: List StateStore/EventStream resources.
- `delete_infra`: Delete a StateStore/EventStream resource.

**Platform & Cluster**
- `get_platform_status`: Cluster/platform health (Flux, Crossplane, Gateway).
- `list_clusters`: List local kind clusters.
- `use_cluster`: Switch context to a kind cluster.

**Observability**
- `get_app_logs`: Fetch recent logs via Loki; falls back to `shoulders logs` if Loki is unavailable.
- `get_trace`: Fetch a trace by ID from Tempo.

## Resources

The server exposes Crossplane schemas and example manifests as MCP resources:

- `shoulders://schemas/workspace`
- `shoulders://schemas/webapplication`
- `shoulders://schemas/state-store`
- `shoulders://schemas/event-stream`
- `shoulders://examples/workspace`
- `shoulders://examples/webapplication`
- `shoulders://examples/state-store`
- `shoulders://examples/event-stream`

## Environment Variables

- `SHOULDERS_REPO_ROOT`: Path to the Shoulders repo (used to resolve schemas/examples).
- `SHOULDERS_OBSERVABILITY_NAMESPACE`: Observability namespace (default `observability`).
- `SHOULDERS_LOKI_SERVICE`: Loki service name (default `loki`).
- `SHOULDERS_TEMPO_SERVICE`: Tempo service name (default `tempo`).
- `SHOULDERS_LOKI_REMOTE_PORT`: Loki service port (default `3100`).
- `SHOULDERS_TEMPO_REMOTE_PORT`: Tempo service port (default `3100`).
- `SHOULDERS_MCP_PORT_FORWARD_TIMEOUT_MS`: Port-forward timeout in ms (default `15000`).
- `KUBECTL_BIN`: Path to `kubectl` (default `kubectl`).
- `KUBECONFIG`: Kubeconfig to target.

## Notes

- `get_trace` requires a trace ID. For trace search/discovery, use Grafana Explore with the Tempo data source.
- `get_app_logs` uses Loki when available; the fallback streaming mode is truncated after 8 seconds.
