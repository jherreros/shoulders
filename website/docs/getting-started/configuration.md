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
    version: "1.20.2"
  mesh:
    enabled: false        # experimental: shared network + per-cluster pod CIDR
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

## Topologies (hub and spoke)

`clusters:` replaces the single `cluster:` block, which desugars to a one-entry
topology with role `hub`. Each entry overlays the shared `platform:` block and
states only what differs; `role` selects the tier (`hub` = platform, `spoke` =
workload). The hub owns the domain (`platform.domain`, or `domain` on the hub
entry); a spoke declares none and serves workload hostnames qualified with its
own name (`<app>.<cluster>.<domain>`).

```yaml
platform:
  profile: small
  mesh:
    enabled: true
    encryption: wireguard

clusters:
  - name: hub
    role: hub
    domain: hub.127.0.0.1.sslip.io
  - name: spokea
    role: spoke
    ports: { http: 8080, https: 8443 }
```

`platform.mesh.enabled: true` joins the clusters into a Cilium ClusterMesh.
`platform.mesh.encryption: wireguard` encrypts pod-to-pod traffic and must be
the same on every cluster; node encryption stays off. Network policy is
cluster-local by default; a cross-cluster rule names
`io.cilium.k8s.policy.cluster`. The measured design, prerequisites and limits
are in the repository's `docs/multi-cluster-target.md` and
`docs/multi-cluster-evidence.md`.

Global flags: `--config`, `--set key=value`, `--cluster <name>`,
`--kubeconfig`, `--output table|json|yaml`.
