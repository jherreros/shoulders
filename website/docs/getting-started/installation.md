# Installation

## Install the CLI

With Homebrew:

```bash
brew install jherreros/tap/shoulders
```

Or via the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/jherreros/shoulders/main/scripts/install.sh | bash
```

## Prerequisites

- Docker (for local `vind` clusters)
- Free host ports `80` and `443` — Shoulders maps vind container ports 80/443 to your
  host to enable local routing for Dex, Grafana and Headlamp.
- `kubectl`, `helm` for advanced workflows (airgap vendoring needs `skopeo` too).
