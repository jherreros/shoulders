# Quickstart

```bash
shoulders up
```

This will:

1. Create a local vind cluster named `shoulders`.
2. Install Cilium CNI with kube-proxy replacement and Gateway API support.
3. Bootstrap FluxCD.
4. Deploy all platform components via GitOps and wait for reconciliation.

Verify and explore:

```bash
shoulders status
shoulders workspace create team-a
shoulders workspace use team-a
shoulders app init my-app --image nginx
shoulders logs my-app
shoulders dashboard   # Grafana (gateway host, falls back to port-forward)
shoulders portal      # Headlamp (gateway host, falls back to port-forward)
shoulders reporter    # Policy Reporter UI
```

Stop and resume without re-provisioning (local vind only):

```bash
shoulders stop
shoulders start
```

Tear down:

```bash
shoulders down
```

Next: [Configuration](./configuration.md) and [Workspaces](../concepts/workspaces.md).
