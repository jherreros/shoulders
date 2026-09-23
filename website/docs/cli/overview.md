# CLI overview

The `shoulders` CLI is the primary interface for bootstrap and daily workflows.
Reference pages under this section are **auto-generated** in CI from cobra
(`shoulders docs`) — do not edit generated files by hand.

```bash
shoulders init
shoulders up
shoulders down
shoulders start
shoulders stop
shoulders status
shoulders sync
shoulders vendor

shoulders workspace create|list|use|current|delete
shoulders app init|update|apply|build-image|load-image|list|describe|delete
shoulders workload worker|job|cron|list
shoulders infra add-db|add-bucket|add-stream|list|delete
shoulders cluster list|use
shoulders logs <app-name>
shoulders dashboard
shoulders portal
shoulders reporter
shoulders skill install
```

Global flags: `--config`, `--set key=value`, `--kubeconfig`, `--output table|json|yaml`.
Most namespace-scoped commands accept `-n <namespace>` or use the active workspace.

Regenerate locally:

```bash
./scripts/generate-cli-docs.sh
```
