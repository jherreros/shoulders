# Multi-Cluster Target — Hub and Spoke

Date: 2026-10-17; last measured 2026-10-21. Status: **implemented on `vind` and
measured for the laptop target (hub + one spoke).** Real infrastructure (Talos
on Hetzner) is deferred. This document defines *what* the multi-cluster target
is and records what measurement changed; the implementation log is
[`multi-cluster-evidence.md`](./multi-cluster-evidence.md).

Scope note: the primary goal today is a **reference implementation**. The
ambition is that it stays honest enough to eventually run on real
infrastructure (e.g. Talos on Hetzner). That fight is explicitly deferred.

---

## 1. Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | **Roles, not modes.** A topology is a list of clusters, each with a `role`. Monocluster is the degenerate case of one cluster with `role: hub`. No `single`/`multi` flag exists anywhere. | Cost of supporting both comes from mode-flags (a boolean consulted everywhere, two paths that drift), not from having >1 cluster. A role is also more useful: role→profile is how `dev-hub` / `prod-hub` would later be expressed. |
| D2 | **The `cluster:` config block becomes a compat shim**, desugaring into a one-cluster topology at config-load time. | Keeps the shim in one place instead of branching at every call site. |
| D3 | **Platform tier in the hub, workload tier in the spokes.** Platform = IAM, observability, GitOps, control-plane surface (policy is per cluster — D16). Workload = `WebApplication`, `Workload`, `Workspace`, `StateStore`. | The line is platform-vs-workload, not shared-vs-local. Keeps spoke compositions untouched and makes monocluster literally "hub with a spoke attached", so today's `small` profile needs no reinterpretation. |
| D4 | **Deferred: hub-hosted event streams.** `EventStream` stays spoke-local, exactly as today. The XRD is to be shaped so cluster-awareness is additive later. | The original intent was sound — many services consume the same topics, so per-spoke Kafka means N 3-broker clusters for one logically-shared stream. Deferred because it forces **cross-cluster secret distribution**, a whole subsystem, and nothing else in this target depends on it. Analysis retained in §5 so it is not lost. |
| D5 | **Crossplane hybrid, thin hub accepted.** Spokes keep today's local compositions. The hub owns spoke registration and whatever cross-cluster abstractions arrive later. | Spoke-local compositions are already idiomatic Crossplane v2 (namespaced XRs composing third-party CRs), so nothing there needs rewriting. With D4 deferred the hub's Crossplane is thinner still — spoke registration is effectively its only phase-1 job. A stated limitation, not an oversight — see §6. |
| D6 | **Per-cluster Flux, one monorepo, `pathPrefix: clusters/<name>`.** Also Flux Operator's own fleet convention. **Superseded in part by D17:** per-cluster Flux and one monorepo hold; user space is delivered from the shared `3-user-space/` tree rather than a per-cluster path, because a tenant is a tenant on every spoke and copying it per cluster name is the drift D6 was meant to prevent. | Nearly free — `platform.flux.pathPrefix` is already threaded through `internal/config/types.go`. Hub-managed remote Kustomizations would make the hub a single point of reconciliation failure and require hub→spoke API reachability. |
| D7 | **Spoke-local ingress.** Each spoke keeps its own Cilium `Gateway` + `HTTPRoute`. The hub has no role in application traffic. | Cross-cluster Gateway API does not exist in a usable form (Cilium's Gateway support is single-cluster). |
| D8 | **Spokes are `vind` only for now.** `role` stays orthogonal to `provider`. | Nothing in the topology model hardcodes vind. Keeps the Talos/Hetzner door open without building it. CAPI is **not** adopted now — see §7. |
| D9 | **Repo layout unchanged.** Roles are expressed as profile directories under `2-addons/profiles/`. | Avoids a large manifest move that breaks the numbered layering in the README. |
| D10 | **Laptop target is hub + 1 spoke.** | See §4. |
| D11 | **Cilium ClusterMesh is the inter-cluster network.** It provides the encrypted, authenticated transport and the identity primitives; **R1's cross-cluster policy use case turned out not to exist (D21).** | See §3 for what it gives and §8 for what it costs. Proven working on vind at 0% packet loss with WireGuard; the six CLI prerequisites (R1c) are implemented and verified. |
| D12 | **CLI targets clusters explicitly.** A `--cluster` flag backed by a cluster-name resolver replaces the process-global `kube.SetContextOverride`. Ambient current-cluster is kept only as a fallback. | **Implemented.** The resolver derives the context from the topology, so the ~40 existing call sites follow `--cluster` without being rewritten. |
| D13 | **Observability data flow is strictly spoke→hub.** Alloy agents in spokes push to hub backends; the hub never scrapes into a spoke for telemetry. | One-directional means the hub needs **zero** spoke credentials for telemetry. |
| D14 | **Topology config overlays a shared `platform:` block.** Each `clusters:` entry states only what differs; resolution produces a fully-populated per-cluster value. | Compact, and consumers read a flat resolved value rather than reimplementing overlay rules. Zero value means "inherit"; pointer fields exist where "unset" and "false" must differ. |
| D16 | **Admission control runs in both tiers.** Kyverno and its policies are installed on the hub *and* on every spoke; what stays central is the policy *source* in git. | A policy engine only sees the pods admitted by its own kube-apiserver, so a hub-only Kyverno enforces nothing on a spoke. It is also a hard dependency: the `Workspace` composition emits a Kyverno `ClusterPolicy`, so a spoke without the CRD never reconciles its first `Workspace`. The hub keeps its engine for its own platform pods. Measured — evidence §17.2. |
| D17 | **User space is delivered by Flux, on the workload tier.** `3-user-space/` is reconciled by two Kustomizations (`user-space-workspaces`, then `user-space-apps`), and a hub deletes both. | It was the last thing nothing delivered: the compositions existed, the sample tenant existed, and no Kustomization pointed at either. Two, not one, because a `Workspace` owns its namespace, so the tenant's namespaced XRs cannot apply until it is ready. A monocluster is a hub, so this keeps its behaviour unchanged. |
| D18 | **A workload's host is `<app>.<cluster>.<hub domain>`.** The hub owns the only domain; a spoke qualifies it with its own name via `SHOULDERS_WORKLOAD_DOMAIN`. | Two spokes cannot claim the same hostname, and no cluster needs a second DNS zone. The variable is set per cluster by the CLI, so the tenant manifest has no cluster name in it. |
| D19 | **Spoke telemetry is push-only, and metrics-only where the profile has no logs.** A spoke runs its own `kube-state-metrics` and an `alloy-agent` that remote-writes to the hub's Prometheus with `cluster=<name>`. The hub's own series are **unlabelled**: `externalLabels` do not apply to locally stored samples, so the convention is **empty `cluster` = hub**, and the label is kept for federation and alerting. | Kube-state-metrics is what answers "is this workload running on that spoke" without a scrape path into the spoke, and it needs no CRDs. The remote-write label is what makes one query span the topology. The agent and the collector are the symmetric half of D16: the hub removes them. Measured — evidence §17.6. |
| D15 | **Roles are labels, never behaviour.** Host ports, mesh enablement and profile are explicit config. Nothing branches on a role. | The direct test of D1, and the answer to the original concern about complexity: `TestRolesAreLabelsOnly` fails if a role ever starts implying behaviour. |
| D20 | **Secrets are SOPS + age, distributed by the CLI.** `shoulders secrets init` generates the identity (in the sops CLI's own key file, outside the repo); `up` publishes it as `flux-system/sops-age` on every cluster and renders `decryption.provider: sops` on every Kustomization. Decryption is enabled only when a key exists. | Moves no secret over the network and adds no service: the file rides the artifact flux already pulls and the key lives in the cluster flux already runs. A secret store would be a second distributed system to bootstrap and reach across clusters, which is the problem the mesh work spends its effort avoiding. Measured — evidence §18. |
| D21 | **Network policy is cluster-local by default.** `clustermesh.policyDefaultLocalCluster: true`; a rule that must reach another cluster names it with `k8s:io.cilium.k8s.policy.cluster`. | No shipped composition needs cross-cluster scope: every stateful dependency (CNPG, Garage) is installed in every cluster, so the workspace's egress is local. The wide default let a bare namespace selector admit hub traffic without saying so. A real cross-cluster rule still works when it names the cluster, but it can no longer happen by accident. Measured — evidence §19. |

---

## 2. What goes where

### Hub (platform tier)

- **IAM** — Dex, the single OIDC issuer for the topology
- **Observability** — Prometheus, Grafana, Loki, Tempo, Alertmanager; Alloy agents run *in the spokes* and push here
- **Policy** — Kyverno
- **GitOps** — Flux, one install per cluster (D6)
- **Control-plane surface** — CLI, MCP server, Headlamp entry point

### Spoke (workload tier)

- `WebApplication`, `Workload`, `Workspace`, `StateStore`, `EventStream` —
  today's compositions, unmodified
- Cilium CNI + ClusterMesh agent, Flux, Crossplane, CNPG, Garage
- Alloy agents shipping logs and traces to the hub
- Local Cilium `Gateway` + `HTTPRoute` (D7)
- A Headlamp instance (each needs its own OIDC client registered in hub Dex)

### Nowhere

- No shared Postgres — `StateStore` stays spoke-local
- No shared Kafka — `EventStream` stays spoke-local (D4 deferral)
- No shared object storage — `Garage` stays spoke-local

The general rule: **no stateful service is shared across the topology.** Every
stateful abstraction (`StateStore`, `EventStream`, Garage) is spoke-local, so no
spoke depends on the hub for data-path availability.

---

## 3. ClusterMesh: what it buys us

D11 provides the encrypted transport and resolves R2; D21 resolves R1.

**R1 (network policy) — the requirement was wrong, and the correction matters.**
The original plan was for `workspace-composition` to gain a cross-cluster egress
rule selecting the hub with `k8s:io.cilium.network-name`. Measurement showed
three things (evidence §19):

1. Endpoints carry no such label. The cluster label is
   `io.cilium.k8s.policy.cluster`, and pinning it does select exactly one
   cluster.
2. The original "proof" was produced by the **first** rule in the test policy —
   same namespace — while `clustermesh.policyDefaultLocalCluster` was `false`.
   That setting makes a namespace-only policy span the whole mesh.
3. No shipped composition needs cross-cluster scope. Every stateful dependency
   (CNPG, Garage) is installed in each cluster, so the workspace's egress is
   local.

The setting is therefore **`true`** (D21): a namespace-only policy is local by
default, and a future rule that genuinely has to reach another cluster must name
it:

```yaml
egress:
  - toEndpoints:
      - matchLabels:
          k8s:io.cilium.k8s.policy.cluster: hub
    toPorts:
      - ports: [{ port: "3100", protocol: TCP }]
```

With the safe default, a bare namespace selector cannot widen to the mesh
accidentally. Cross-cluster selection still works when asked for explicitly;
it is just never the default.

**R2 (certificates) — largely solved, and the answer reuses existing code.**
Cilium's ClusterMesh uses mTLS between clusters and offers three cert methods:
`certgen` (CronJob), `cert-manager`, and `helm`. The `helm` method generates
certs on every install/upgrade and needs no extra controller — which is the only
realistic option inside a vCluster.

Critically, **all clusters must share a common root CA**, and Cilium's own docs
distribute it by copying a secret:

```sh
kubectl --context hub get secret -n kube-system cilium-ca -o yaml \
  | kubectl --context spoke create -f -
```

That is a hub→spoke CA distribution — and it is structurally the same operation
Shoulders already performs in `bootstrap/domain.go:50` (`GenerateDexTLSMaterial`
mints a CA and `up.go` pushes it into `authentication-config.yaml` and
`dex-tls-secret.yaml`). So: the CLI mints the ClusterMesh trust-domain root once
and pushes `cilium-ca` into every cluster before mesh enablement. This converges
D11 and the Dex/OIDC cert path onto one mechanism — roughly 80% of a platform PKI
falls out of the machinery you already have.

**What R2 does *not* solve.** Alloy's `tls { insecure = true }` stays true.
ClusterMesh provides WireGuard encryption and mutual auth **at the network
layer** for pod traffic, so the application-level plaintext is no longer
plaintext on the wire. Node encryption is deliberately off (the initial
opt-out split broke the cold-install mesh and telemetry paths; evidence §20), so
this applies to pod traffic, not to host traffic. That is acceptable for local
dev and must be documented as "encrypted by the mesh, not by the application" —
it is not the same as app-level mTLS.

---

## 4. Laptop feasibility

`small` today = 37 pods (measured healthy) and already contains Dex, Grafana,
Prometheus and Headlamp. Note this cuts the other way from what was first
assumed: a hub owning **real** observability converges near `medium`, which per
`CONTRIBUTING.md` is where this hardware starts to fail. Deferring D4 keeps the
hub's *component set* exactly at `small`, but the hub is still a full-weight
platform cluster.

- **`hub-small`** = today's `small`, **unchanged** — 37 pods, the measured figure.
  Because D4 is deferred the hub needs neither Strimzi nor the `EventStream`
  XRD/composition.
- **`spoke`** ≈ Cilium + ClusterMesh + Flux + Crossplane + CNPG + Garage.
  ≈15–20 pods.
- Total for hub + 1 spoke ≈ **52–57 pods across two control planes**.

**Measured on this host (2026-10-21).** Two clusters are the ceiling. Adding a
third spoke joined the mesh and started its platform reconcile, then all three
control planes saturated the 11-CPU Docker VM at 180–360% CPU each; the APIs
reset connections, Kyverno's fail-closed webhook began rejecting every write,
and dropping the third control plane restored both APIs within seconds. The
third spoke's Cilium, CNPG and Garage installed; its Flux platform did not
finish. That is a hardware limit, not a topology bug: the code supports three
(evidence §21). A third spoke needs a larger host or a deliberately lighter
spoke profile. Pod WireGuard on the two-cluster pair did not disturb the tenant
or cross-cluster traffic. The cold run did find that node encryption's
control-plane opt-out split breaks the mesh and telemetry host paths; node
encryption is now off (evidence §20).

The payoff of deferring D4 is that `hub-small` is **literally today's `small`
profile, unchanged** — the strongest available confirmation that monocluster and
multi-cluster genuinely share one profile definition rather than two
near-identical ones. Adding Loki/Tempo on top (full LGTM) remains the stretch
goal, not the laptop default.

Disk headroom in `internal/bootstrap/preflight.go` (`ProfileDiskMinimum`) is flat
per-cluster (25/40/60 GiB). It must become topology-aware or a legitimate
hub+spoke layout will be refused.

---

## 4a. What exists today

Worth stating plainly, because the design record and the implementation have
diverged before:

**Implemented and measured.** A topology model (`clusters:`, decisions D1/D2/D14)
where each entry overlays a shared `platform:` block and roles are labels only
(D15). A resolved cluster carries its own profile, domain, Cilium/Flux settings,
mesh settings and host ports, and those ports are rendered into the vend values
— so two vind clusters now coexist, which was impossible before. Plus the
ClusterMesh prerequisites (shared Docker network, distinct pod CIDRs, CoreDNS
entries) and a fix for the CoreDNS restart label. The current state — tier split,
tenant delivery, per-cluster ingress, SOPS, local-by-default policy and WireGuard
— is measured in `docs/multi-cluster-evidence.md` §15–§22.

**Also implemented.** D12: a global `--cluster` flag with a resolver that derives
the kubeconfig, context and profile from the topology. Every command reads the
resolved cluster, so `--cluster spoke-a` works uniformly without touching the
~40 existing call sites. `shoulders cluster list` renders the topology, and
`cluster use` writes back to the right entry (a topology entry, not a competing
legacy `cluster:` block).

**Install orchestration is built.** `shoulders up` loops the topology one
cluster at a time: it creates the vind cluster, seeds `cilium-ca`, wires
`cluster.name`/`id` and the mesh values, installs the tier's addons through Flux,
sets the gateway host, and validates. The local OCI source is pushed once per
cluster because a vind cluster's registry is cluster-local. The hub+spoke pair is
the measured target; a third spoke reaches the mesh but exceeds this host's CPU
(evidence §21).

## 5. Deferred: hub-hosted event streams (D4)

**Decision: deferred.** `EventStream` remains spoke-local and unchanged. The
analysis is kept here so the decision can be revisited with its reasoning intact.

**The original argument.** In a real deployment many services consume the same
topics, so per-spoke Kafka means N 3-broker clusters for one logically-shared
stream. That is a real inefficiency and the instinct was right.

**Why it is deferred.** Putting Kafka in the hub forces **cross-cluster secret
distribution** — a whole subsystem. Today `infra add-stream` + `EventStream`
creates a Kafka in the *current* cluster and consumers reach it via in-cluster DNS
(`strimzi-kafka-bootstrap.<ns>`); none of that machinery crosses a cluster
boundary. A Kafka connection secret created in the hub must reach workloads in
spokes, and nothing in Shoulders does that today.

Nothing else in this target depends on D4. Deferring it removes the single
largest piece of work from the critical path.

**Dex is the wrong tool for the credential path.** Dex is an OIDC provider for
interactive human login; its token API issues *identity* tokens, not connection
secrets. Minting Kafka passwords from it would mean either Dex static passwords
(a hardcoded shared secret with extra steps) or writing a custom Dex storage
backend. Dex's role is unchanged from today: authenticate the developer for
Headlamp/Grafana SSO, with spokes acting as remote OIDC clients.

Options for when it is revisited, cheapest first:

| | Approach | Cost |
|---|---|---|
| 1 | **SOPS + GitOps** — Kafka secret encrypted in the repo; every cluster's Flux decrypts it | No new runtime component, fits the existing Flux-centric model. Secrets in git, even encrypted. |
| 2 | **Bespoke CLI-generated secret + hub→spoke distribution** — CLI generates credentials and pushes Secrets | No new runtime component, but this *is* a secret-distribution feature with no other purpose |
| 3 | **External Secrets Operator + hub `ClusterSecretStore`** (e.g. Vault) | Industry standard, but two meaningful new components |
| 4 | **SPIFFE/SPIRE identities + broker-side authz** — the credential *is* the pod's mesh identity | Elegant, and ClusterMesh already establishes the trust domain, but Strimzi + SPIRE is nontrivial |

**SOPS (1) is the expected route** if this is picked up, because it fits the
existing Flux-centric architecture better than any alternative.

**Revisit trigger:** when a second workload in a different spoke needs the same
topic. Until then the N-broker-cluster cost is acceptable and the alternative is
a subsystem nobody needs.

**Constraint on the XRD, honoured now:** `EventStream` must be shaped so that
cluster-awareness can be added later as a **non-breaking change** — a defaulted,
additive field. Do not let the current spoke-only shape bake in an assumption
that the Kafka is always local.

---

## 6. Crossplane thinness and the "single API surface" question

Concretely, each Crossplane owns:

- **Spoke:** `Workspace`, `WebApplication`, `Workload`, `StateStore`,
  `EventStream` + today's 5 compositions + 3 functions +
  `crossplane-composed-resources.yaml` RBAC. **Completely unchanged.**
- **Hub:** spoke registration, plus whatever cross-cluster abstractions arrive
  later. With D4 deferred, the hub's Crossplane has no workload abstractions of
  its own in phase 1.

The hub's Crossplane is thin, and with D4 deferred it is thinner than originally
framed: **spoke registration is effectively its only phase-1 job.** The hard part
of hub-spoke — *"a developer asks for a resource, the platform decides which
cluster hosts it, creates it, and delivers working credentials into a workload in
a different cluster"* — is not attempted in this phase. That is §5, deferred, and
§6's stated limitation.

**However: most of the single-API-surface benefit is cheap.** A read-only fan-out
in the CLI gives `shoulders app list` across all spokes with a `CLUSTER` column.
The `provider-kubernetes` management-cluster pattern is only needed for *writes
that must land in another cluster*.

**Docs framing, to be stated explicitly rather than discovered later:**
*Shoulders is a workload-tier IDP with a shared platform tier, not a unified
multi-cluster control plane.* Developers still `kubectl` into whichever spoke
holds their workspace.

### CLI targeting (D12)

Scale of the change: **42 call sites** of the `kubeconfig` global across 8 command
files (`app.go` 5, `infra.go` 5, `workspace.go` 4, `workload.go` 4, `sync.go` 3,
`status.go` 2, `up.go` 1, `logs.go` 1), all fed by the process-global
`kube.SetContextOverride(cfg.Cluster.Context)` at `cmd/root.go:114`, plus
`ensureShouldersCluster` (`root.go:128`) validating exactly one context.

D12 replaces that with a **cluster-name resolver**, plumbed as `--cluster`.
Mechanical, and identical behaviour at N=1 — which is what makes it safe to do
before the topology exists. Ambient `cluster use` survives as a fallback so
monocluster UX is untouched.

Rejected alternatives, for the record: *ambient-only* switching is unscriptable
(`for c in spokes; do shoulders app list; done` mutates global state each
iteration); *workspace→cluster resolution first* needs the mapping correct before
any command exists, so it comes after the topology lands.

Two consequences: the **MCP server needs the same resolver**
(`shoulders-mcp-server/src/utils.ts:104`), and since MCP clients connect to one
server, a **single fleet-aware server** serving N clusters is the only sane
option.

---

## 7. Spoke registration and CAPI

Distinguish two things that are easy to conflate:

- **Cluster *creation*** is necessarily CLI-driven — only the CLI can run vind.
- **Registration*** should be declarative: a `Spoke` CR in the hub, so the
  topology is self-describing and Flux-reconcilable. The CLI path becomes a thin
  write of that CR.

Do one well rather than both. The declarative CR is the interesting one.

**CAPI is not adopted now.** Its value is provisioning real infrastructure (Talos
VMs, cloud); for vind it is redundant, since vind is already a container. Adding
it now is a large dependency for no local benefit. **Keep `provider` as the seam**
(D8) so `provider: capi` can arrive later alongside Talos/Hetzner.

---

## 8. Open risks

### R1a — vind prerequisites: RESOLVED by measurement, with corrections *(was: highest)*

> **Status: measured. See [`multi-cluster-evidence.md`](./multi-cluster-evidence.md).**
> ClusterMesh **works** on vind — cross-cluster pod-to-pod demonstrated at 0% loss
> both directions. But it required **six** changes Shoulders does not make today,
> and the assessment in the previous revision of this section was wrong in two
> places.

**Correction 1 — the pod CIDR collision is real and it breaks the datapath.**
This section previously argued that tunnel mode tolerates overlap because
ClusterMesh identifies endpoints by `(cluster ID, IP)`. **That is disproven.**
Confirmed from the pinned source *and* measured:

- `chart/values.yaml:1169` — `networking.podCIDR: "10.244.0.0/16"`
- `pkg/k8s/k8s.go:182` — passed as `--cluster-cidr` to kube-controller-manager
- vind forces private nodes, so this applies to every cluster
- `1-cluster/vind-config*.yaml` never sets it

So **every vind cluster created today gets an identical pod CIDR.** With the
collision in place the mesh control plane connects, but cross-cluster pod traffic
fails at 100% loss and the remote pod never appears in `cilium-dbg endpoint list`.
Fix: set `networking.podCIDR` per cluster. One line.

**Correction 2 — a larger blocker was missed entirely.** vind creates an
**isolated Docker bridge network per cluster** (`vcluster.<name>`, 172.19.0.0/16 vs
172.20.0.0/16). Node InternalIPs are mutually unreachable, which violates a
hard ClusterMesh prerequisite outright. Fix: `experimental.docker.network`, which
the pinned vCluster supports and which is idempotent.

**Verified working configuration:** `experimental.docker.network` (shared),
`networking.podCIDR` (distinct), `cilium-ca` pre-seeded before install,
`cluster.name`/`cluster.id`, `authMode: legacy`, `service.type: NodePort`,
`tls.auto.method: helm`, `extraDnsNames` with `<cluster>.mesh.cilium.io`, and
CoreDNS mesh entries merged into the existing `hosts` block. Two traps that cost
time and are documented in the evidence log: `LoadBalancer` service type needs
sudo, and a second CoreDNS `hosts` block crashes CoreDNS.

**Superseded — see R1b and evidence §19.** The "selector accepted but traffic
still permitted" result was not evidence about the label: the remote allowance
came from the policy's same-namespace rule while
`policyDefaultLocalCluster` was `false`. The cluster selector is
`io.cilium.k8s.policy.cluster`, and it works when tested directly.

### R1b — Network policy scope: measured, corrected, resolved

> **Status: measured.** The §8.1 result in the evidence log was invalid; the
> correction and the new measurement are in evidence §19.

The test policy had two rules: same namespace, and a cluster label. The first
one is what admitted the remote pod, because the runtime setting was `false`.
That setting makes a namespace-only policy span the mesh. Replacing the second
rule with a real cluster selector produced these results, first with the old
setting and then with `policyDefaultLocalCluster: true`:

| Policy | Local (spokea→spokea) | Remote (hub→spokea) |
|---|---|---|
| namespace only, setting `false` | allowed | **allowed** — the widening |
| namespace only, setting `true` | allowed | **blocked** |
| `policy.cluster: spokea` | allowed | blocked |
| `policy.cluster: hub` | blocked | allowed |

So:

- The cluster label is `io.cilium.k8s.policy.cluster`. Endpoints carry no
  `io.cilium.network-name` label, and the old proof never tested the label.
- `policyDefaultLocalCluster: true` makes a namespace-only policy local. Every
  stateful dependency is local, so this is the correct default (D21).
- An explicit cluster pin still selects exactly that cluster. A future
  cross-cluster rule works; it just has to say so.

The old caveat 2 still holds: use a namespaced `CiliumNetworkPolicy`, not
`CiliumClusterwideNetworkPolicy`; the workspace composition already does. Its
Kyverno naming rule targets `ClusterPolicy`, so a future cluster-scoped policy
addition would trip it.

### R1c — ClusterMesh CLI prerequisites: 6 of 6 fixed *(was: none existed)*

Full detail in the evidence log, §10 and §10a. **Implemented and verified:**
shared Docker network (`experimental.docker.network`), per-cluster
`networking.podCIDR`, per-cluster host ports, CoreDNS mesh entries merged into
the existing `hosts` block, and the CoreDNS restart label. All driven by the
resolved cluster rather than hand-patched manifests, and opt-in behind
`platform.mesh.enabled` (default false) for the networking parts.

**Also implemented (this was the "next phase"):** `cilium-ca` pre-seeding across
clusters, `cluster.name`/`cluster.id`, and the four ClusterMesh Cilium values
(`authMode: legacy`, `service.type: NodePort`, `tls.auto.method: helm`,
`extraDnsNames`). `shoulders up` applies them through
`bootstrap.ClusterMeshOptions`, and the mesh forms on every topology install.
Measured in evidence §12–§20.

### R1d — `EnsureClusterDNS` never restarts CoreDNS — **FIXED**

`internal/bootstrap/dns.go` deleted CoreDNS pods with
`LabelSelector: "k8s-app=kube-dns"`. In vCluster the actual label is
`k8s-app=vcluster-kube-dns`, so the delete matched nothing, returned success,
and the Corefile change silently never took effect. This affected the existing
`--local` dev loop independent of multi-cluster work.

Now `CoreDNSPodSelector` matches both conventions, and `restartCoreDNS` lists
first and returns an error when nothing matches — the silent-no-op failure mode
can no longer recur.

### R2a — ClusterMesh establishes a *flat* trust domain *(architectural)*

Cilium's docs are explicit: *"All clusters connected in a Cluster Mesh form a
single trust domain... Only connect clusters that are mutually trusted and have
equivalent security postures."*

This is in tension with hub-and-spoke, which usually implies hierarchy and trust
separation. ClusterMesh over a hub-and-spoke topology produces a **flat security
boundary presented with a hierarchical shape** — a compromised spoke can affect
the hub.

**Status: accepted for the reference implementation, explicitly deferred to the
Hetzner/Talos phase.** This is a known limitation, not an oversight. When real
infrastructure arrives and spokes need to be untrusted, ClusterMesh cannot
express that and the mesh plane must be replaced (option 4 above, or a
network-policy-only model that never federates identity). Do not let this be
rediscovered during the Hetzner work — it is recorded here precisely so it is
addressed then.

### R3 — Host ports and per-cluster hostnames — **RESOLVED**

All three `1-cluster/vind-config*.yaml` hardcode
`experimental.docker.ports: ["80:80", "443:443"]`, so only one vind cluster could
own host ingress. This blocked any multi-cluster install outright.

Now each `clusters:` entry carries an explicit `ports:` mapping, rendered by
`manifests.VindValues` into the vend values. Two clusters coexist:

```
mesha  443/tcp=0.0.0.0:443  80/tcp=0.0.0.0:80
meshb  8080/tcp=0.0.0.0:8080  8443/tcp=0.0.0.0:8443
```

Because roles are labels (D15), nothing infers "the hub gets 80/443": the ports
are declared. Validation rejects two `vind` clusters claiming the same host port,
while allowing `provider: existing` clusters to share one — those bind nothing,
since the CLI did not create them.

`platform.domain` remains global rather than per-cluster, so two clusters sharing
a domain would still collide on hostnames. That is now the only part of R3 left,
and it is a naming concern rather than a hard conflict.

### R4 — CLI, MCP server, portal plugin are single-cluster *(high, D12)*

See §6. D12 scopes the CLI/MCP work. The **portal plugin is inherently
per-cluster** (it is a Headlamp plugin), so D7+D8 mean N Headlamps, each needing
its own OIDC client registered in hub Dex — which is N clients to mint, rotate and
revoke.

### R5 — Profile mechanism will not scale to roles *(medium)*

Profiles are "full manifest set minus delete-patches"
(`2-addons/profiles/small/helm-releases/delete-*.yaml`). Adding `hub` and `spoke`
overlays gets unmaintainable fast, and the `hub`/`spoke` split is *orthogonal* to
`small`/`medium`/`large` — a second axis the delete-patch scheme cannot express.

Likely resolution: split `2-addons/manifests/` into per-capability bases (`cni`,
`gitops`, `idp`, `o11y`, `gateway`, `workload-abstractions`) and let role × profile
compose them. Deliberately deferred (D9), recorded as known debt.

### R6 — Thin hub Crossplane *(medium, accepted)*

With D4 deferred the hub's Crossplane owns only spoke registration in phase 1 —
see §6. Stated limitation of the reference implementation, not a defect to fix
here. It becomes substantive only when a cross-cluster abstraction is actually
needed.

### R7 — Hub observability blind to spoke control planes *(low, D13)*

D13 keeps telemetry strictly spoke→hub, which is a genuine win (zero spoke
credentials in the hub) but means **the hub only sees what spokes push**. Spoke
control-plane metrics (apiserver, etcd, kube-state-metrics) must therefore be
pushed too, or you get application telemetry without cluster telemetry — an odd
dashboard. Needs an explicit decision on whether each spoke runs a
scraping-and-pushing agent, or whether hub-scraping is added back as a deliberate
hub→spoke exception.

**Note: the TSDB choice is a separate decision and does not fix R7.** Whatever
backend is chosen, the fix is the same: the spoke agent must scrape control-plane
objects and push them. Choosing Mimir or VictoriaMetrics changes nothing about
coverage. Keep them apart — see §8a.

---

### R8 — Flux reconciles Cilium: *keep it*, but close the value gap *(was: delete it)*

**Found by measurement, not inspection** — see evidence §12.8.

**Correction.** An earlier draft of this risk concluded that `cilium.yaml`
should be deleted from the Flux-managed set, arguing that "Flux can never
legitimately own Cilium's lifecycle" because Flux cannot reconcile anything until
the CNI is up. **That reasoning was wrong**, and overreached in a way worth
recording: the dependency constrains *startup ordering*, not *ownership*. The
endorsed pattern is exactly the opposite of what was claimed — bootstrap the CNI
imperatively, then let Flux adopt it:

> "You can install a CNI plugin at cluster bootstrap with the Helm CLI, then Flux
> will take over the release if you define a HelmRelease with the [same name]"
> — [fluxcd/flux2 discussion #3070](https://github.com/fluxcd/flux2/discussions/3070)

And this repository is already built for that pattern. `cilium.yaml` is
`name: cilium`, `namespace: kube-system` — **identical** to the CLI's Helm
release, so helm-controller adopts it instead of creating a second one. There is
no `suspend` and no `driftDetection` override. Meanwhile **Renovate owns the
version in git** (commits `c6bf7ec`, `25f3598` moved it to 1.20.2) while the CLI
carried a stale `1.19.2`. That drift is not evidence of a mistaken layering
decision; it is the predictable result of getting the version wrong under
*either* architecture.

So the risk is real but misdiagnosed. The problem is not that Flux manages
Cilium — it is that **the manifest is an incomplete description of what the CLI
actually installs**.

#### What the collision actually caused

| | CLI value | `cilium.yaml` | Result on adoption |
|---|---|---|---|
| `kubeProxyReplacement` | `true` | absent | silently reverted to `false` |
| `k8sServiceHost` / `Port` | discovered per cluster | absent | agents lose the API endpoint |
| `extraConfig.api-rate-limit` | set | absent | dropped |
| `cluster.name` / `id`, `clustermesh.*` | per cluster (mesh) | absent | **the mesh is erased** |

This also resolves **incidental finding 4** in the evidence document: the manifest
genuinely omits `kubeProxyReplacement`, rather than the README overstating things.

#### The fix: split value *ownership*, not value *precedence*

The blocker for adopting the user's pattern was that three of those values
cannot be written in git at all:

- `k8sServiceHost` / `k8sServicePort` — read from each cluster's own API endpoint
  at install time.
- `cluster.name` / `cluster.id` — this cluster's identity in the mesh.
- `clustermesh.config.clusters` — the peer list, which is per topology.

So the CLI now publishes exactly those keys to a ConfigMap
(`shoulders-cilium-cluster`) and the HelmRelease references it:

```yaml
valuesFrom:
  - kind: ConfigMap
    name: shoulders-cilium-cluster
    valuesKey: values.yaml
    optional: true
```

Two properties make this safe rather than a layering compromise:

- **The key sets are disjoint.** `ipam`, `gatewayAPI`, `prometheus`, `hubble`,
  `envoy`, `image` stay in git; the runtime keys above stay in the ConfigMap.
  This matters because **flux merges inline values *over* `valuesFrom`** — an
  overlapping key in git would silently win over what the CLI discovered. Rather
  than depend on precedence, the design removes the precedence question.
- **`optional: true` keeps the non-CLI path working.** The README documents
  `kubectl apply -k 2-addons/profiles/<profile>/flux`; there is no CLI there to
  write the ConfigMap, and that path has always relied on the inline values.

This also reuses a convention the repo already has: `postBuild.substituteFrom:
shoulders-platform-config` is a CLI-written ConfigMap feeding Flux.

#### Version drift

`DefaultCiliumVersion` is now `1.20.2`, matching git, and a test fails if they
diverge again. Under this architecture the CLI's copy is the *starting point*
and git is the *destination*, so a mismatch means every install is silently
upgraded one sync interval later — a confusing way to discover a version bump.
Renovate owns the git copy; the constant must move with it.

#### The exclusion that turned out to be the real problem

The recommendation above — "keep it, but close the value gap" — was half right
and missed something bigger. The CLI was **deliberately deleting the Cilium
HelmRelease from Flux's set on every install**:

```go
// Always suspend the Flux-managed Cilium HelmRelease.
bootstrap.SuspendCiliumHelmRelease(kubeconfig)
```

That was a kind-to-vind migration leftover (c3fc83b) that had become permanent.
Its consequences were worse than the gap it was avoiding:

- GitOps was the *nominal* owner of the CNI while never reconciling it, so
  `2-addons/manifests/helm-releases/cilium.yaml` was dead weight.
- **Renovate's Cilium version bumps never reached a running cluster** — which
  is what the "version drift" was actually measuring.

Replaced by `SetCiliumHelmReleaseFluxOwnership(kubeconfig, fluxOwns)`: Flux owns
Cilium when it is enabled; when it is disabled (`provider: existing` default)
the cluster has its own CNI and the exclusion is applied in that case only.

#### Status

**Implemented and validated end to end** on a real `shoulders up --local`: Flux
creates and adopts the Cilium HelmRelease, `kubeProxyReplacement`,
`k8sServiceHost` and the `clustermesh` block all survive, the CLI and Flux
agree on the version (no surprise upgrade), and there are zero `UpgradeFailed`
events. See evidence §13 and §14.

## 8a. Metrics backend: Prometheus vs Mimir vs VictoriaMetrics

**This is orthogonal to the multi-cluster work** and gates nothing in it. It can be
decided and implemented today, monocluster, without waiting for the topology. It
is recorded here because R7 made it topical.

**What changes with D13.** Today the hub scrapes its own cluster. Under D13 the hub
becomes a *pure ingestion and aggregation layer* — it no longer needs to scrape.
That is a meaningfully different shape of requirement, and it is the thing that
should drive the choice.

**What Shoulders actually needs.** A laptop, 2–3 clusters, ~50–60 pods. The
configured retention ceiling today is `large` = 7 days / 5 GiB. This is
thousands of series, orders of magnitude below the ~1M datapoints/sec where
distributed backends start to matter. **Scalability is not a criterion here;
operational simplicity is.**

| | Prometheus (today) | Grafana Mimir | VictoriaMetrics |
|---|---|---|---|
| Shape | Single binary, but not a production-grade remote-write receiver | `mimir-distributed` is multi-component; monolithic mode available | Single binary; cluster mode is `vminsert`+`vmselect`+`vmstorage` |
| Multi-cluster fit | Needs Thanos on top for aggregation | Built for federated ingest | `vmselect` reads **multiple single-node instances and deduplicates automatically** — a natural cluster-per-topology fit |
| Long-term storage | None (local volume only) | Object storage (S3) | Local volume; `vmbackup` for snapshots |
| Grafana compatibility | Native | Native | Native, PromQL-compatible — existing dashboards, ServiceMonitors, recording and alerting rules work unchanged |
| Hub-down resilience | n/a | n/a | `vmagent` buffers on disk (`-remoteWrite.maxDiskUsagePerURL`) |

**Recommendation: VictoriaMetrics for the reference implementation, not Mimir.**

1. **Single binary.** It matches the project's laptop budget and the
   reference-implementation goal far better than a multi-component distributed
   backend. VictoriaMetrics' own docs recommend the single-node version below
   ~1M dps and advise "think twice before choosing the cluster version."
2. **The multi-cluster shape fits.** `vmselect` pointing at one single-node VM per
   cluster, with automatic deduplication, is close to exactly the topology here —
   and it lets each spoke keep a local TSDB, which keeps queries fast and makes
   hub outages non-fatal for recent history.
3. **`vmagent`'s on-disk buffering** directly addresses a problem D13 *creates*:
   spoke agents must survive hub outages. The project has a documented history of
   DiskPressure (see `docs/platform-feedback-roadmap.md` §1.2 A3), so
   bounded buffering matters more here than usual.
4. **Low CPU and memory**, against a hardware budget already known to be tight.
5. **Zero migration cost** — PromQL-compatible, so every existing dashboard,
   ServiceMonitor, recording rule and Alertmanager rule carries over.

**When Mimir would be the right answer:** if the Grafana-ecosystem standard
matters more than operational simplicity, if you need PromQL-level multi-tenancy
isolation, or if the Talos/Hetzner deployment will be operated by people who
already know Thanos/Mimir. Note `Garage` is already in the stack and is
S3-compatible, so the object storage both Mimir and Thanos require is available
today — that removes the main objection to keeping them as the production path.

**Possible follow-on:** VictoriaLogs is a Loki analogue from the same project. If
Loki centralisation is ever revisited, the same reasoning applies. Not in scope now.

---

## 9. Not decided

### Blocking the next phase

Nothing. R1b is resolved (D21) and WireGuard is enabled and measured; the
remaining items are independent of the topology work.

### Done since this section was first written

The topology model (D1/D2), the orchestration, the ClusterMesh prerequisites
(R1a/R1c), the Cilium hand-off (R8), the tier split (D3) and user-space
delivery (D17) are implemented and measured — evidence §12–§22.

- **Each spoke has its own Headlamp** and its own Dex client, verifying the
  hub's issuer against a CA the CLI now generates once per topology (§16).
- **D13 is measured, not assumed.** A spoke's `kube-state-metrics` and
  `alloy-agent` remote-write to the hub's Prometheus; `count by (cluster)
  (kube_pod_info)` returns the spoke's series labelled `cluster="spokea"`
  (§17.6). An empty cluster label means "hub".
- **A tenant runs on a spoke, and only there**: `Workspace`, `WebApplication`,
  `StateStore` and `Workload` all `READY=True`, reachable through the spoke's
  own gateway, with no `team-a` namespace on the hub (§17.7).
- **The tier is symmetric.** The spoke drops the platform tier; the hub drops
  the workload tier's telemetry and user-space Kustomizations. The rules are
  Go (`internal/bootstrap/tier.go`), and the tests read the manifests so an
  addon added without an exclusion fails in CI, not in a cluster.
- **Secrets are safe in git.** `shoulders secrets init` + `sops -e`, with flux
  decrypting in-cluster from `flux-system/sops-age`; verified by reading a
  decrypted value from a running pod (§18). The sample keeps its secret
  optional, because the key is per machine.
- **Network policy is local by default (D21).** The old §8.1 proof was invalid;
  re-measurement showed the same-namespace rule, not the cluster label, was
  admitting hub traffic. With `policyDefaultLocalCluster: true`, namespace-only
  egress no longer crosses the mesh, while an explicit
  `io.cilium.k8s.policy.cluster` selector still reaches the named cluster
  (evidence §19).
- **Pod WireGuard is on, and measured; node encryption is off.** Pod-to-pod
  traffic between hub and spoke is encrypted, the agent reports `Peers: 3`, and
  every node reports `NodeEncryption: Disabled`. The first implementation
  enabled node encryption; the cold install put the two clustermesh-apiservers
  on opposite sides of Cilium's control-plane opt-out, and host-bound mesh and
  telemetry traffic was dropped. Disabling it restored both, with pod traffic
  still encrypted and the mesh control plane already mTLS (evidence §20).
  Applying a changed Cilium config on a Flux-owned release needed more than a
  restart: the CLI now forces the HelmRelease, waits for a newer deployed Helm
  revision, writes the encryption switches the chart omits, and only then rolls
  the agents. Unchanged values remain a no-op.

### Remaining, in the order it blocks things

- **A third spoke at `small`** — the code supports it and it joined the mesh,
  but the full platform reconcile on a third control plane saturated this
  laptop and stalled all three APIs (evidence §21). Needs a larger host or a
  lighter spoke profile; D10 remains hub + 1 spoke.
- **R5 beyond Cilium** — the profile mechanism itself, and the profiles' role
  in a topology, are untested.
- **R7** — spoke control-plane metrics, and the §8a metrics backend, are
  untouched. The VictoriaMetrics multi-instance dedup claim is still
  **unverified** (evidence §9.1).
- **D6 literally** — `pathPrefix: clusters/<name>` is honoured as an
  operator-facing option but is not the default: a default would tie the
  repository to the cluster names the operator picks. The tier difference is
  expressed as patches instead (§15.3, superseded by D17 for user space).

### Independent of the topology work

- Spoke control-plane metrics strategy (R7 — orthogonal to the TSDB choice)
- Metrics backend: VictoriaMetrics vs Mimir vs staying on Prometheus (§8a). The
  VictoriaMetrics multi-instance dedup claim is **unverified** (evidence log §9.1).
- Whether Flux Operator is adopted later for fleet UX (not required by D6)
- Whether `Workspace` stays cluster-scoped or becomes namespaced under D1
- What the hub's Crossplane owns once a real cross-cluster abstraction is needed
  (§5 revisit trigger: a second workload in a different spoke needing the same
  topic)

---

## 10. Evidence status

| Risk | Status |
|---|---|
| R1a — vind mesh prerequisites | **Proven live** (evidence §12.7). All six fixes hold; two earlier assumptions corrected along the way. |
| R1b — network policy scope | **Resolved and re-measured** (evidence §19). The original proof was invalid: the allowance came from a same-namespace rule with `policyDefaultLocalCluster: false`. The cluster label is `io.cilium.k8s.policy.cluster`; the safe local default is now set (D21), and an explicit cross-cluster pin still works. |
| R1c — six missing CLI changes | **6 of 6 fixed and verified live** (§12.7). CA pre-seeding, `cluster.name`/`id` and the mesh values are wired through `cmd/upcluster.go` and `bootstrap.ClusterMeshOptions`. |
| R1d — `EnsureClusterDNS` label bug | **Fixed.** Selector matches both conventions; restart fails loudly instead of silently no-opping. Two further defects found by the live run and fixed: a race patching the Corefile before CoreDNS exists (now `ErrCoreDNSNotRunning`, so pre-CNI seeding is allowed and post-CNI is still loud), and **records inserted after a directive, which crash-looped CoreDNS outright** (§12.5). |
| R2a — flat trust domain | Accepted for reference impl; deferred to Hetzner. |
| R3 — host ports and per-cluster hostnames | **Resolved.** Ports are per cluster and validated. The domain is now **hub-owned** (decision (a)): the hub declares it, a spoke declares none and serves the platform hostnames. The validator requires the hub to declare one in a topology and rejects two clusters declaring the same one. A spoke's own Headlamp is served at `headlamp-<cluster>.<domain>`. |
| R4 — CLI/MCP single-cluster | **Resolved.** CLI via D12; the MCP server now takes `SHOULDERS_CLUSTER` and fails loudly on an unknown cluster instead of falling back to the kubeconfig's current-context. |
| R5 — profile mechanism | **Cilium's half resolved by moving it into git (§13.3).** The profile's observability (`dashboards.enabled`, `hubble.{relay,ui}`) is now expressed in `2-addons/profiles/<profile>/helm-releases/cilium.yaml`, not computed by the CLI, because flux merges the manifest's inline values over the CLI's. The CLI and the manifest are now asserted to agree on the static values they share. The rest of R5 (profiles as a general mechanism) is still untested. |
| R6 — thin hub Crossplane | Accepted by decision. |
| R7 — hub blind to spoke control planes | Not tested. |
| D3 — the platform/workload split | **Implemented and measured** on a real two-cluster topology (§15): the hub runs the platform tier, the spoke the workload tier, the mesh forms, and traffic crosses it both ways with 0% loss. Each cluster has its own Headlamp, and the spoke's authenticates against the hub's Dex over the mesh (§16). |
| R8 — Flux reconciles Cilium | **Resolved.** The exclusion was deliberate, not a bug: `SuspendCiliumHelmRelease` (a kind→vind migration leftover, c3fc83b) deleted the HelmRelease from Flux's set on every `up`. Removed; Flux now owns the CNI and Renovate's bumps reach it. Proven end to end (§14.5). |
| D13 — spoke telemetry | **Measured** (§17.6). A spoke's `kube-state-metrics` + `alloy-agent` remote-write to the hub's Prometheus; `count by (cluster) (kube_pod_info)` returns 41 spoke series labelled `cluster="spokea"` (and 8 of them in `namespace="team-a"`). Hub-local series carry no label: **empty cluster = hub**. |
| D16 — admission control on every tier | **Implemented.** Kyverno is a workload-tier resource. The `Workspace` composition emits a Kyverno `ClusterPolicy`, so the first tenant `Workspace` requires the engine on the spoke; the policy reconciles there (§17.2). |
| D17 — user space per cluster, from one tree | **Implemented and measured** (§17.7). Two Kustomizations on a spoke (`user-space-workspaces`, then `user-space-apps`) and none on a hub. The local OCI artifact now packages `3-user-space/`; before that the Kustomization pointed at a path the artifact did not contain. |
| D20 — SOPS + age | **Implemented and measured** (§18). The CLI generates the identity and publishes it as `flux-system/sops-age` before the Kustomizations; flux decrypts a `*.sops.yaml` Secret in the tenant tree, and the value is visible in a running pod. Decryption stays off when no key exists. |
| D21 — local-by-default network policy | **Implemented and measured** (§19). A namespace-only policy no longer reaches the hub; an explicit `io.cilium.k8s.policy.cluster` pin selects exactly one cluster. No shipped policy needs the wide scope. |
| WireGuard encryption | **Enabled and measured** (§20). Pod-to-pod traffic is encrypted across the mesh (`Peers: 3`). Node encryption is off on every node: the control-plane opt-out split broke the cold-install mesh/telemetry host paths, and the mesh control plane is mTLS anyway. Applying changed values on a Flux-owned release forces the HelmRelease, waits for a newer deployed revision, repairs keys Helm leaves stale, then rolls the agents. |
| D10 — laptop ceiling | **Validated by measurement** (§21). Hub + 1 spoke is stable; a third spoke's control plane overwhelmed this host, and the APIs recovered when it was removed. The code supports three; the laptop does not. `down` 18s, cold `up` 12m28s, idempotent `up` 3m05s (§22). |

### Cluster resolution now has one answer

The first live orchestration run (evidence log §12.1) showed that the codebase
resolved "which cluster?" in two independent ways: the `internal/kube` package
honours a context override, while Helm reads the ambient kubeconfig
current-context. These agree in a monocluster install and **must** agree, which
is why the divergence was invisible until two clusters existed — the Cilium
release landed in one cluster while its operator was configured against the
other's API server, and hung `pending-install` forever.

`targetClusterContextName()` in `cmd` is now the single derivation, and
`CiliumOptions.ContextName` carries it into both Helm entry points. Worth
retaining as a rule: **any new component that resolves a cluster must go through
that helper**, not through the current kubeconfig.

### What the live run changed about the plan

The topology install is written and verified (§12.7), but four defects surfaced
that no amount of reading would have found, and they are worth naming because
they share a shape — a value that is correct in isolation and wrong in
combination:

| Defect | Symptom | Why it was invisible before |
|---|---|---|
| Helm ignores the context override | Cilium installed into the wrong cluster, hung `pending-install` | only one cluster existed |
| Records appended after a Corefile directive | CoreDNS crash-looped; the cluster lost DNS | vCluster's own Corefile has no records to reorder |
| `IP:port` in the peer address | endpoints like `https://10.90.0.2:32379:32379`; mesh never formed | the agent connected anyway; only KVStoreMesh failed |
| Release existence treated as installed | a failed install became permanent | monocluster runs rarely fail mid-install |

Two lessons carried into the code rather than just the document:

- **Check `KVStoreMesh` replicas, not just the agent.** `cilium clustermesh
  status` reported the agent as connected while KVStoreMesh was the component
  that had actually failed.
- **When a value has been measured, the implementation follows the measurement.**
  The doubled-port bug used an IP where §7.2 had already recorded a DNS name,
  because the IP looked more direct. The evidence existed; it just was not read.

The measurements live in [`multi-cluster-evidence.md`](./multi-cluster-evidence.md),
including everything that was tried and *failed*, so the dead ends are not
re-walked.