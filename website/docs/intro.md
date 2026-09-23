# Shoulders

Shoulders is a reference implementation of an Internal Developer Platform (IDP) that
demonstrates how to use Crossplane to provide a self-service platform for developers
to create and manage cloud-native applications and infrastructure on Kubernetes.

The name originates from _"If I have seen further it is by standing on the shoulders
of Giants"_ by Isaac Newton. Applications on Shoulders run on the shoulders of the
maintainers and contributors of all the open-source tools it composes.

## What you get

- **Workspaces** — isolated tenant environments with network policies and naming conventions.
- **Web Applications** — containerized apps with Deployments, Services and Gateway API routing.
- **Workloads** — background workers, one-shot Jobs and CronJobs.
- **State Stores** — PostgreSQL (CloudNativePG), Redis and Garage S3 buckets with independent toggles.
- **Event Streams** — full Kafka clusters and topics via Strimzi.
- **Observability** — LGTM stack (Loki, Grafana, Tempo, Prometheus) pre-configured.
- **Security** — Kyverno, Trivy Operator, Falco and Policy Reporter.
- **Three interfaces** — CLI, Headlamp developer portal, and MCP server for AI assistants.

All resources are Crossplane Composite Resources managed through Flux GitOps.

Start with [Quickstart](./getting-started/quickstart.md).
