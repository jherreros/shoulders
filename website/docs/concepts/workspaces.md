# Workspaces

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
- A default-deny **CiliumNetworkPolicy** allowing only intra-workspace, kube-system and
  cnpg-system traffic.
- A **Kyverno ClusterPolicy** enforcing that all workload names are prefixed with the
  workspace name (e.g. `team-a-*`).

CLI:

```bash
shoulders workspace create <name>
shoulders workspace list
shoulders workspace use <name>
shoulders workspace current
shoulders workspace delete <name>
```
