# Copilot instructions for Shoulders

## Big picture architecture
- The repo is a 3-layer platform: `1-cluster/` creates a local vind (vCluster-in-Docker) cluster, `2-addons/` installs platform services via FluxCD, and `3-user-space/` contains team-facing examples.
- Flux kustomizations in `2-addons/flux/kustomizations.yaml` enforce install order: helm repositories → namespaces → helm releases → crossplane → gateway.
- Crossplane defines the IDP abstractions in `2-addons/manifests/crossplane/definitions/` (XRDs) and implements them with compositions in `2-addons/manifests/crossplane/compositions/`.
- The `shoulders` CLI (`shoulders-cli/`) is available via `brew install jherreros/tap/shoulders` or the install script. An agent skill (`.github/skills/shoulders/`) provides deployment guidance for AI assistants.

## Crossplane composition patterns
- Compositions use **Pipeline** mode and functions. Patch/transform is used for static resource wiring (`function-patch-and-transform`), while dynamic resource generation uses `function-go-templating` (see `state-store-composition.yaml`).
- `application-composition.yaml` maps `WebApplication` specs into a Deployment, Service, and Gateway API `HTTPRoute` with `spec.host` → `spec.hostnames[0]` and a `cilium-gateway` parent ref.
- `workspace-composition.yaml` creates a Namespace, a default-deny CiliumNetworkPolicy, and a Kyverno ClusterPolicy that prefixes workload names with the workspace name.
- `state-store-composition.yaml` conditionally emits CloudNativePG, Redis, and Garage bucket resources with Go templates; note the fixed `app-secret` secret for PostgreSQL. Because `StateStore` is namespaced, Garage bucket Jobs and their helper ServiceAccount/Role/RoleBinding/CiliumNetworkPolicy are composed into the workspace namespace and write per-bucket S3 credential Secrets there.

## Developer workflows
- Cluster bootstrap: `1-cluster/create-cluster.sh` creates a vind cluster named `shoulders`.
- Platform install: `2-addons/install-addons.sh` installs Cilium via Helm, ensures Flux CLI is present, then `flux install` + `kubectl apply -f 2-addons/flux/`.
- Observability access: README documents Grafana port-forward (`svc/kube-prometheus-stack-grafana` in `observability`).

## Conventions and integration points
- All developer-facing resources live in `3-user-space/`; use the examples in `3-user-space/team-a/` as canonical templates (e.g., `webapp.yaml`).
- Most XRDs are **Namespaced** (see `application-xrd.yaml`), so example resources should always set `metadata.namespace` to a workspace namespace. `Workspace` is cluster-scoped per `workspace-xrd.yaml`.
- Platform components are declared as Flux-managed manifests under `2-addons/manifests/` (helm repos/releases, namespaces, gateway, crossplane).
- Crossplane RBAC for composed resources is defined in `2-addons/manifests/crossplane/rbac/crossplane-composed-resources.yaml`—add permissions here when compositions introduce new APIs.
- When adding a platform component end to end, update all four surfaces: Flux-managed addon manifests under `2-addons/manifests/`, Crossplane XRD/composition/RBAC, user-facing examples/docs/skills, and the CLI/MCP/portal helpers that generate manifests. Garage is the reference pattern for components that need a platform service plus per-workspace provisioning Jobs; validate namespaced Crossplane behavior at runtime because explicit `metadata.namespace` on composed resources is ignored for namespaced XRs in Crossplane v2.
