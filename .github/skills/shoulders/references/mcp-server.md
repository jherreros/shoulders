# MCP Server (Optional)

If your agent has the Shoulders MCP server configured, you can use it as an alternative to CLI commands. The MCP server provides the same operations via structured tool calls.

## Tool Mapping

| MCP Tool | CLI Equivalent |
|----------|----------------|
| `create_workspace` | `shoulders workspace create` |
| `use_workspace` | `shoulders workspace use` |
| `list_workspaces` | `shoulders workspace list` |
| `current_workspace` | `shoulders workspace current` |
| `delete_workspace` | `shoulders workspace delete` |
| `deploy_app` | `shoulders app init` |
| `list_apps` | `shoulders app list` |
| `get_app_status` | `shoulders app describe` |
| `delete_app` | `shoulders app delete` |
| `add_database` | `shoulders infra add-db` |
| `add_bucket` | `shoulders infra add-bucket` |
| `add_stream` | `shoulders infra add-stream` |
| `list_infra` | `shoulders infra list` |
| `delete_infra` | `shoulders infra delete` |
| `get_app_logs` | `shoulders logs` |
| `get_platform_status` | `shoulders status` |
| `list_clusters` | `shoulders cluster list` |
| `use_cluster` | `shoulders cluster use` |

## Configuration

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

## When to Use

The CLI is preferred when both are available — it provides richer terminal output and `--dry-run` support. Use the MCP server when:

- The agent doesn't have terminal access
- You need structured JSON responses for programmatic use
- You want to access Crossplane XRD schemas via MCP resources
