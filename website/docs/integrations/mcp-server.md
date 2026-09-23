# MCP server

Shoulders includes an MCP server (`shoulders-mcp-server/`) for AI assistants and
MCP-compatible clients. It talks directly to the Kubernetes API and integrates with
Loki and Tempo.

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

Tools include workspace/app CRUD, infra provisioning, platform status, cluster
management, Loki logs and Tempo traces. Crossplane schemas and example manifests are
exposed as MCP resources.

See `shoulders-mcp-server/README.md` for the full tool list.
