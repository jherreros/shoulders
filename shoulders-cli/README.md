# shoulders-cli

Developer CLI for the Shoulders Internal Developer Platform. The bootstrap flow uses Go-native APIs (vCluster library + Helm SDK) rather than shelling out to scripts.

## Install

With Homebrew:

```bash
brew install jherreros/tap/shoulders
```

Or via the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/jherreros/shoulders/main/scripts/install.sh | bash
```

## Build

```bash
cd shoulders-cli
go mod tidy
go build -o shoulders
```

## Usage

### Cluster Management
```bash
./shoulders init --provider vind                         # Write a starter config at ~/.shoulders/config.yaml
./shoulders init --provider existing --config ./cfg.yml # Write a starter config for an existing cluster
./shoulders up                        # Create and bootstrap the platform (default name: shoulders)
./shoulders up --verbose              # Same, with detailed per-phase progress
./shoulders --config ./cfg.yml up     # Install onto the cluster selected in a config file
./shoulders cluster list              # List running vind clusters or kube contexts
./shoulders cluster use dev           # Switch context to 'dev' cluster or kube context
./shoulders down --name dev           # Delete a local vind cluster
./shoulders --config ./cfg.yml down   # Uninstall Shoulders from an existing cluster
./shoulders update                    # Check for and install a new CLI version
```

### Workspace Management
```bash
./shoulders workspace create team-a
./shoulders workspace list
./shoulders workspace use team-a
./shoulders workspace current
./shoulders workspace delete team-a
```

### Application Lifecycle
```bash
./shoulders app init hello --image nginx:1.26 --replicas 1
./shoulders app list
./shoulders app describe hello
./shoulders logs hello
./shoulders app delete hello
```

### Infrastructure
```bash
./shoulders infra add-db app-db --type postgres --tier dev
./shoulders infra add-bucket app-assets --bucket team-a-assets --secret team-a-assets-s3
./shoulders infra add-stream events --topics "logs,events" --partitions 3 --replicas 3 \
	--config cleanup.policy=compact
./shoulders infra list
./shoulders infra delete app-db
```

### Platform
```bash
./shoulders status                    # Show cluster & platform health
./shoulders status --wait             # Poll until all components are healthy
./shoulders dashboard                 # Opens the configured Grafana host (defaults to grafana.localhost)
./shoulders portal                    # Opens the configured Headlamp host (defaults to headlamp.localhost)
./shoulders reporter                  # Opens the configured Policy Reporter host (defaults to reporter.localhost)
./shoulders skill install             # Install the Shoulders agent skill for AI assistants
./shoulders skill install --workspace # Install into the current project instead of globally
```

The default `*.localhost` access pattern requires host port `80` to be available when the cluster is created. If you changed these settings, recreate the cluster:

```bash
./shoulders down
./shoulders up
```

## Configuration
The CLI reads `~/.shoulders/config.yaml` by default, or another file via `--config`.

Example schema:

```yaml
current_workspace: ""

cluster:
  provider: vind          # vind | existing
  name: shoulders
  kubeconfig: ""
  context: ""

platform:
  domain: ""            # Optional suffix like lvh.me -> grafana.lvh.me, headlamp.lvh.me, dex.lvh.me
  cilium:
    enabled: true
    version: "1.19.2"
  flux:
    gitRepository:
      url: "https://github.com/jherreros/shoulders.git"
      branch: "main"
    pathPrefix: "."
```

Behavior:
- `provider: vind` keeps the current local-cluster workflow.
- `provider: existing` targets an already running cluster and uses `cluster.context` when set.
- Cilium defaults to enabled for `vind` and disabled for `existing`.
- When Cilium is disabled, Gateway route health is treated as externally managed and `up`/`status` do not block on a Cilium Gateway.
- `platform.domain` remaps the public hosts together: `dex.<domain>`, `grafana.<domain>`, `headlamp.<domain>`, `reporter.<domain>`, `prometheus.<domain>`, `alertmanager.<domain>`, and `hubble.<domain>`.
- `platform.flux.gitRepository.url`, `branch`, and `pathPrefix` let Flux reconcile the Shoulders manifests from a different repository, branch, or subdirectory.
- `down` deletes the local cluster for `vind`, and removes the Flux-managed Shoulders platform for `existing`.
- `start` and `stop` are only meaningful for local vind clusters.

## Output formats
Use `-o table|json|yaml` for supported list and status commands.

## Notes
- `shoulders app init` supports `--dry-run` to emit YAML instead of applying it.
- `shoulders logs` attempts a Loki query first and falls back to direct pod log streaming (no `kubectl`).
- `shoulders up` provisions the cluster via the vCluster Go library (vind/Docker driver) and installs Cilium + Flux without running shell scripts. It pulls the Cilium chart and Flux install manifest from their upstream URLs.
- `shoulders up --verbose` shows detailed descriptions for each bootstrap phase.
- `shoulders up` displays a live timer, per-phase durations, and a final summary (e.g. "Shoulders platform provisioned in 04:32").
- `shoulders reporter` opens the configured Policy Reporter host, defaulting to `reporter.localhost`, and falls back to a local port-forward on port 8082.
- `shoulders infra add-bucket` creates a StateStore object bucket backed by Garage and writes S3 credentials to a workspace Secret.
- `shoulders infra add-stream` supports `--partitions`, `--replicas`, and repeatable `--config key=value` entries.
- `shoulders up` and `down` support `--name` to create/delete specifically named clusters.
- `shoulders status --wait` polls every 3 seconds and refreshes the TUI display until all components are healthy.
- `shoulders update` checks the latest GitHub release and self-updates the binary.
- Commands that interact with the cluster verify a Shoulders vind context when `provider: vind`, or use the configured kube context when `provider: existing`.
