# Configuration

The CLI reads `~/.shoulders/config.yaml` by default. Use `--config <path>` to point at a
different file, and repeatable `--set key=value` flags to override file values.
`--set` overrides are **ephemeral**: they apply to that invocation only and are
not saved to the config file (`up` prints a reminder). Edit the file for
durable changes — e.g. `platform.profile: medium`, without which
`infra add-stream` refuses.

Generate a starter file:

```bash
shoulders init --provider vind
shoulders init --provider existing --config ./shoulders.yaml
```

Example:

```yaml
current_workspace: ""

cluster:
  provider: vind          # vind | existing
  name: shoulders
  kubeconfig: ""
  context: ""

platform:
  profile: medium        # small | medium | large
  cilium:
    enabled: true
    version: "1.19.2"
  flux:
    source: git             # git | oci
    gitRepository:
      url: "https://github.com/jherreros/shoulders.git"
      branch: "main"
    ociRepository:
      url: ""
      tag: ""
      insecure: false
    pathPrefix: "."
```

Notes:

- `provider: existing` skips cluster creation and targets the configured kube context.
- `platform.profile` selects the addon footprint (see [Profiles](../guides/profiles.md)).
- `platform.flux.*` lets you point Flux at a different repo, branch, subdirectory, or OCI artifact.
- `--set` supports `current_workspace`, `cluster.provider`, `cluster.name`,
  `cluster.kubeconfig`, `cluster.context`, `platform.profile`, `platform.domain`,
  `platform.cilium.enabled`, `platform.cilium.version`, `platform.flux.*`.

Global flags: `--config`, `--set key=value`, `--kubeconfig`, `--output table|json|yaml`.
