# Shoulders Portal

A [Headlamp](https://headlamp.dev) plugin that exposes the **Shoulders** Internal Developer
Platform catalog directly in your Kubernetes dashboard. Teams get a single pane of glass
to browse and inspect every platform resource created through the Crossplane-backed
[Shoulders](https://github.com/jherreros/shoulders) platform.

## Features

The plugin adds a **Shoulders** entry to the Headlamp sidebar (under the Cluster section)
and surfaces four resource types:

| Resource | Description |
|---|---|
| **Workspaces** | Cluster-scoped workspace foundations and guardrails |
| **Web Applications** | Deployments with ingress, routing, and scaling |
| **State Stores** | PostgreSQL and Redis services for teams |
| **Event Streams** | Kafka-backed topic bundles for streaming workloads |

## Prerequisites

- [Headlamp](https://headlamp.dev) deployed in-cluster (or desktop)
- The [Shoulders](https://github.com/jherreros/shoulders) platform installed on the cluster
  (provides the `shoulders.io/v1alpha1` CRDs via Crossplane)

## Installation

Install via the Headlamp `pluginsManager` by adding the following to your Headlamp Helm
release values:

```yaml
plugins:
  - name: shoulders-portal
    source: https://artifacthub.io/packages/headlamp/shoulders-portal-plugin/shoulders-portal
    version: 0.0.5
```

## Usage

Once installed, navigate to the **Shoulders** entry in the Headlamp sidebar. Each tab
lists the corresponding platform resources from the cluster, with status and spec details
rendered inline.

## Contributing

Source code and contribution instructions are available at
[github.com/jherreros/shoulders](https://github.com/jherreros/shoulders).
