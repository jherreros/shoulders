# Deploy onto an existing cluster

Generate an example config, edit it for your target kubeconfig/context, then run `up`
against that file:

```bash
shoulders init --provider existing --config ./shoulders.yaml
shoulders --config ./shoulders.yaml up
```

When `cluster.provider: existing`, `shoulders up` installs the platform onto the selected
cluster instead of creating a vind cluster.

On `provider: existing`, `shoulders down` removes the Flux-managed Shoulders platform
from the current cluster. If `platform.cilium.enabled: true`, it also uninstalls the
`cilium` Helm release from `kube-system`.

Note: `provider: existing` + `--bundle` is rejected — images must be pre-mirrored by
other tooling (the bundle's image list is the manifest to mirror).
