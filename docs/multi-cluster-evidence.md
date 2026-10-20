# Multi-Cluster Evidence Log

Date: 2026-10-18; last measured 2026-10-21. Companion to
[`multi-cluster-target.md`](./multi-cluster-target.md).

This document records **measured results**, not design intent. Its purpose is to
replace assumption with evidence for the risks raised in the target document —
chiefly **R1a** (Cilium ClusterMesh on vind) and **R2** (certificate handling).

Every claim below is reproducible; the exact commands and values are recorded in
§7.

---

## 1. Verdict

**Cilium ClusterMesh works on vCluster-in-Docker (vind).** Cross-cluster
pod-to-pod traffic was demonstrated with 0% packet loss in both directions.

**It does not work out of the box.** Six distinct changes are required, none of
which Shoulders makes today. Four were prerequisite failures that produced hard
errors; two were silent misconfigurations.

The most significant correction to the target document: **R1a was assessed as
"probably not a blocker". That was wrong.** The pod CIDR collision is real and
it breaks the datapath. A *second*, larger blocker was found alongside it —
per-cluster Docker network isolation — which the target document had not
identified at all.

---

## 2. Environment

| | |
|---|---|
| Host | macOS (darwin/arm64), 11 CPUs, 16 GiB RAM |
| Docker | 29.8.2, `overlayfs` |
| Disk at start | 51 GiB free (89% used) |
| vCluster | chart `0.33.1` via `loft-sh/vcluster` Go library `v0.37.2` (`VindVersion` in `internal/bootstrap/cluster.go:25`) |
| Cilium | chart `1.20.2`, image `v1.20.2` — matches the repo pin in `2-addons/manifests/helm-releases/cilium.yaml` |
| Kubernetes | `v1.36.0` (the pinned version) |
| Topology | 2 bare clusters, control plane + node **inside a single container each** |

Disk was reclaimed first (`docker builder prune -af`, +23 GiB). Images were
deliberately **not** pruned: vind uses the host Docker daemon as a pull-through
cache, so those images are load-bearing.

---

## 3. Prerequisite failures found

ClusterMesh documents these as hard requirements. Each was tested directly.

### 3.1 Per-cluster Docker network isolation — **VIOLATED** *(not previously identified)*

vind creates a **dedicated, isolated Docker bridge network per cluster**:

```
$ docker network ls
vcluster.mesha    bridge    172.19.0.0/16
vcluster.meshb    bridge    172.20.0.0/16
```

Docker isolates user-defined bridges from each other. A raw TCP listener in
`meshb` was unreachable from `mesha` by IP (`TimeoutError`) and unresolvable by
name (`gaierror`). ClusterMesh requires that "nodes in all clusters must have IP
connectivity between each other using the configured InternalIP for each node."

Node InternalIPs are `172.19.0.2` and `172.20.0.2` — mutually unreachable.

**Fix, confirmed present in the pinned vCluster:** `experimental.docker.network`.
In `config/config.go:3448` ("If not specified, a network will be created for the
vCluster") and `pkg/cli/create_docker.go:933-939`. `createNetwork`
(`create_docker.go:1371-1377`) checks for an existing network first and skips
creation, so it is idempotent and safe to point every cluster at one network.

After applying it:

```
mesha  IP=10.90.0.2  CIDR=10.244.0.0/24
meshb  IP=10.90.0.3  CIDR=10.245.0.0/24
```

Mutually routable. This is a **single values line per cluster** and is the
highest-value change in this document.

### 3.2 Identical pod CIDRs — **VIOLATED** *(previously assessed as low risk)*

From the pinned source, before running anything:

- `chart/values.yaml:1169` — `networking.podCIDR: "10.244.0.0/16"`
- `pkg/k8s/k8s.go:182` — `args = append(args, "--cluster-cidr="+vConfig.Networking.PodCIDR)`
- chart comment — "should only be set if `privateNodes.enabled` is true"

vind forces private nodes, so that default applies to every cluster. Since
`1-cluster/vind-config*.yaml` never sets `networking.podCIDR`, **every vind
cluster the product creates today gets `10.244.0.0/24`.** Confirmed live:

```
mesha  podCIDR=10.244.0.0/24
meshb  podCIDR=10.244.0.0/24     <-- identical
```

And Cilium sees the collision explicitly:

```
$ cilium-dbg node list      # in mesha
Name          IPv4 Address   Endpoint CIDR   Source
mesha/mesha   172.19.0.2     10.244.0.0/24   local
meshb/meshb   172.20.0.2     10.244.0.0/24   clustermesh
```

**Impact, measured:** with identical CIDRs the mesh *control plane* connects
fine, but cross-cluster pod traffic fails with 100% packet loss, and the remote
pod never appears in `cilium-dbg endpoint list`. The datapath cannot disambiguate.

> **Correction to R1a.** The target document hypothesised that tunnel/encapsulation
> mode makes identity `(cluster ID, IP)` and therefore tolerates overlap. **That
> hypothesis is disproven.** Tunnel mode encapsulates to the remote *node*, but
> the agent still cannot resolve which remote cluster a given pod IP belongs to
> when both clusters claim the same range.

**Fix:** set `networking.podCIDR` per cluster. Confirmed working:

```
mesha  CIDR=10.244.0.0/24
meshb  CIDR=10.245.0.0/24
```

---

## 4. Prerequisites that were not in the target document

Three further changes were needed before anything connected. None were
anticipated.

### 4.1 `LoadBalancer` service type is unavailable

vCluster warned at cluster creation:

> `Load balancer type services are not supported inside the vCluster because this
> command was executed with insufficient privileges. To enable load balancer type
> services, run this command with sudo`

The ClusterMesh API server is normally exposed as `LoadBalancer`. Under an
unprivileged CLI, `clustermesh-apiserver` never becomes ready — `helm --wait`
failed with `Available: 1/2`. `NodePort` is required. (Cilium itself warns that
NodePort "may fail when nodes are removed", which is acceptable for local dev.)

### 4.2 The default `migration` auth mode breaks peer authentication

With `authMode: migration` (the chart default) and Helm-generated certs, the
control plane never connected. meshb's etcd logged a continuous rejection loop:

```
"rejected connection on client endpoint","error":"remote error: tls: bad certificate"
```

while kvstoremesh retried every ~90s with
`Unable to list keys before starting watcher ... error="context canceled"`.

The CA was verified identical in both clusters
(`BD:7A:FC:A3:A3:01:6A:...`) and both `clustermesh-apiserver-remote-cert`s were
properly issued (`issuer=CN=Cilium CA`, `subject=CN=remote`) — so this was not a
trust-material problem.

Per the chart's own documentation, `migration` creates per-cluster etcd users
(`clustermesh-remote-users` contained `remote-mesha` and `remote-meshb`) while
still requiring `CN=remote`, which does not line up for the Helm-generated
cert path. **`authMode: legacy` (single shared `remote` user) works.**

Result with `legacy`:

```
✅ All 1 nodes are connected to all clusters [min:1 / avg:1.0 / max:1]
✅ All 1 KVStoreMesh replicas are connected to all clusters
  - meshb: 1/1 configured, 1/1 connected
```

### 4.3 Certificate SANs require DNS names, not IPs

Connecting by shared-network IP failed at the TLS layer:

```
❌ Cannot establish TLS connection: x509: certificate is valid for
   127.0.0.1, ::1, not 172.21.0.3
```

The generated cert carries SANs `clustermesh-apiserver.kube-system.svc`,
`*.mesh.cilium.io`, `<cluster>.mesh.cilium.io`, `127.0.0.1`, `::1` — no IP SANs.
The documentation requires "every DNS name through which remote clusters reach
the Cluster Mesh API Service", so peers **must** connect by name.

**Fix:** resolve `<cluster>.mesh.cilium.io` to the cluster's shared-network IP in
cluster DNS (below), and add it to `extraDnsNames`.

---

## 5. Cluster DNS

Pods resolve via the in-cluster CoreDNS, not Docker's resolver, so mesh names
must be injected there.

Two traps, both hit and both recorded:

**Trap 1 — a second `hosts` block crashes CoreDNS.** The vCluster Corefile already
contains `hosts /etc/coredns/NodeHosts`. Adding another block yields:

```
plugin/hosts: this plugin can only be used once per Server Block
```

and the pod enters `CrashLoopBackOff`. Mesh entries must be **merged into the
existing `hosts` block** as inline entries.

Working form:

```
    hosts /etc/coredns/NodeHosts {
        10.90.0.2 mesha.mesh.cilium.io
        10.90.0.3 meshb.mesh.cilium.io
        ttl 60
        reload 15s
        fallthrough
    }
```

**Trap 2 — see §6.** Restarting CoreDNS needs the right label.

---

## 6. Product bug found: `EnsureClusterDNS` never restarts CoreDNS

`shoulders-cli/internal/bootstrap/dns.go:42`:

```go
if err := clientset.CoreV1().Pods("kube-system").DeleteCollection(ctx,
    metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: "k8s-app=kube-dns"}); err != nil {
```

**The label is wrong.** In vCluster the CoreDNS pods are labelled:

```
$ kubectl get pods --show-labels | grep coredns
coredns-6dc6dfcb8f-7v76f   1/1   Running   k8s-app=vcluster-kube-dns,...
```

`DeleteCollection` matches nothing and returns success, so `EnsureClusterDNS`
**silently patches the Corefile and never restarts CoreDNS**. The fix never takes
effect. This is independent of multi-cluster and affects the existing
`--local` dev loop.

**Fix:** `LabelSelector: "k8s-app=vcluster-kube-dns"` (or select by pod name
prefix `coredns-` for robustness across both label conventions).

This is worth an issue regardless of anything else in this document.

---

## 7. Reproduction

### 7.1 Cluster creation

Driven by `shoulders-cli/internal/bootstrap/mesh_experiment_test.go`, which
reuses the product's own `EnsureVindCluster` (there is no `vind` binary; the
cluster is created through the `loft-sh/vcluster` Go library, and `internal/`
cannot be imported externally — hence a test).

```bash
cd shoulders-cli
MESH_CIDR_MODE=distinct MESH_DOCKER_NETWORK=shoulders-mesh \
  SHOULDERS_MESH_EXPERIMENT=1 \
  go test ./internal/bootstrap/ -run 'TestMeshExperiment$' -v -timeout 30m
```

Teardown (deliberately outside the env gate so failures cannot strand clusters):

```bash
SHOULDERS_MESH_EXPERIMENT_CLEANUP=1 \
  go test ./internal/bootstrap/ -run TestMeshExperimentCleanup -v
```

### 7.2 Working Cilium values

Derived from `2-addons/manifests/helm-releases/cilium.yaml`. Values differing
from the repo are marked.

```yaml
cluster:
  name: mesha                    # ADDED: per cluster
  id: 1                          # ADDED: per cluster, 1-255
ipam:
  mode: kubernetes               # as repo
routingMode: tunnel              # ADDED: see below
protocol: vxlan                  # ADDED: see below
gatewayAPI:
  enabled: true
  hostNetwork:
    enabled: true
clustermesh:
  useAPIServer: true             # ADDED
  policyDefaultLocalCluster: false   # historical; shipped value is true (see §19)
  apiserver:
    service:
      type: NodePort             # ADDED: LoadBalancer needs sudo (§4.1)
    tls:
      authMode: legacy           # ADDED: required (§4.2)
      auto:
        enabled: true
        method: helm             # ADDED: no CronJob/cert-manager in a vCluster
        certValidityDuration: 365
        server:
          extraDnsNames:
            - mesha.mesh.cilium.io   # ADDED: SANs need DNS names (§4.3)
  config:
    enabled: true
    clusters:
      mesha: { address: mesha.mesh.cilium.io, port: 32379 }
      meshb: { address: meshb.mesh.cilium.io, port: 32379 }
operator:
  replicas: 1                    # experiment only: single-node hostPort conflict
```

`routingMode`/`protocol` are **not** currently set in the repo's `cilium.yaml`.
The target document flagged this as a future pin; the evidence here shows tunnel
mode does **not** rescue a pod CIDR collision (§3.2), so an explicit pin is still
worthwhile for determinism but is not the fix.

### 7.3 Install order

1. Helm-install Cilium in the first cluster
2. Copy `cilium-ca` from it into every other cluster **before** their install
   (this is what makes `method: helm` produce a shared trust domain)
3. Helm-install Cilium in the remaining clusters
4. Patch CoreDNS in every cluster, merging mesh entries into the existing
   `hosts` block (§5)
5. Restart CoreDNS with label `k8s-app=vcluster-kube-dns` (§6)

---

## 8. Proven result

```
✅ All 1 nodes are connected to all clusters [min:1 / avg:1.0 / max:1]
✅ All 1 KVStoreMesh replicas are connected to all clusters
```

Cross-cluster pod-to-pod, both directions:

```
p-mesha=10.244.0.233   p-meshb=10.245.0.207

--- mesha -> meshb ---
3 packets transmitted, 3 packets received, 0% packet loss
round-trip min/avg/max = 0.577/0.620/0.672 ms
--- meshb -> mesha ---
3 packets transmitted, 3 packets received, 0% packet loss
round-trip min/avg/max = 0.380/0.478/0.628 ms
```

Round-trip times are sub-millisecond, consistent with two vClusters on one host.

### 8.1 Cross-cluster policy enforcement — superseded by §19

> **Read this as the record of a wrong result, not as evidence.** The first rule
> in the test policy admitted the remote pod while
> `policyDefaultLocalCluster` was `false`; the cluster label was never what
> allowed it. The correction is at the end of this section and in §19.

This is the mechanism the Shoulders workspace egress rule depends on, so it was
tested directly with a policy that models the design: same-namespace plus one
named cluster.

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata: { name: mesh-egress-model, namespace: meshtest }
spec:
  endpointSelector: { matchLabels: { app: probe } }
  egress:
    - toEndpoints: [ { matchLabels: { k8s:io.kubernetes.pod.namespace: meshtest } } ]
    - toEndpoints: [ { matchLabels: { k8s:io.cilium.network-name: meshb } } ]
```

Results from `p-mesha` (10.244.0.233) in `meshtest`:

| Destination | Expected | Result |
|---|---|---|
| `p-other` 10.244.0.139 — other namespace, **same cluster** | blocked | **100% packet loss** |
| `p-meshb` 10.245.0.207 — **cross-cluster**, matched by `network-name` | allowed | **0% loss**, ~0.68 ms |
| `p-other` → `p-meshb` (outside the policy's namespace, ungoverned) | allowed | 0% loss |

So both halves hold: the default-deny genuinely enforces, and
**`k8s:io.cilium.network-name` selects and permits a remote cluster's
endpoints.** That is exactly the substitution §3 of the target document proposes
for the workspace policy — swapping the hardcoded `kube-system` / `cnpg-system` /
`placeholder` namespace labels for the hub cluster's name.

**Why the first attempt looked inconclusive.** A `CiliumClusterwideNetworkPolicy`
using the same selector was accepted and applied, yet traffic survived even when
the selector was rewritten to a non-existent cluster. A same-cluster baseline
test (`local-default-deny`) confirmed policy enforcement *does* work — so the
clusterwide result was a semantics artefact, not a broken mechanism. The
namespaced policy above is the clean result. **Use namespaced policies, not
clusterwide ones, for the workspace model.**

**Correction — see §19.** The label claim above is wrong. Cilium endpoints carry
no `io.cilium.network-name` label; the cluster selector is
`io.cilium.k8s.policy.cluster`. The remote allowance in the table came from the
**first** rule (same namespace) plus `policyDefaultLocalCluster: false`, not
from the second rule. §19 measures the label directly and re-derives the
posture. The conclusion that the workspace needs a cross-cluster egress rule is
also wrong: every stateful dependency (CNPG, Garage) is installed in every
cluster, so the workspace policy is local.

---

## 9. NOT proven — open follow-ups

> This list records the state after the first mesha/meshb experiment. Later
> sections close several items; §19–§21 are the current measurements.

Recorded so they are not mistaken for validated.

1. ~~**The security posture of `policyDefaultLocalCluster: false` is unanalysed.**~~
   **Resolved in §19.** The flag is not needed: every stateful dependency is
   local, so the safe default (`true`) is now set. Namespace-only policies no
   longer span the mesh; a future cross-cluster rule must name the cluster with
   `io.cilium.k8s.policy.cluster`.
2. **Use namespaced `CiliumNetworkPolicy`, not `CiliumClusterwideNetworkPolicy`,
   for the workspace model.** The clusterwide variant behaved inconsistently in
   testing (§8.1); the namespaced variant enforces correctly. The existing
   `workspace-composition` already emits a namespaced `CiliumNetworkPolicy`, so
   this aligns with current code — but the Kyverno naming policy in the same
   composition targets `ClusterPolicy`, so any cluster-scoped addition would hit
   the prefix rule.
3. **The `medium` and `large` profiles were not used.** Resource impact is now
   measured for the two-cluster `small` target (§15–§20) and for a failed third
   `small` spoke (§21); `medium`/`large` remain untested. Single vs. two-node
   topology is no longer a gap: the mesh was validated on the `small` profile's
   control-plane + 1 worker, including worker-to-worker cross-cluster traffic.
5. **`kube-proxy-replacement` was left at its chart default (`false`)**, matching
   the repo's `cilium.yaml`, which does not set it — despite the README claiming
   kube-proxy replacement. Not investigated; noted as a possible doc/code
   discrepancy.
6. **No Hubble, no Gateway API, no Dex** were installed *in this first
   experiment*. That is no longer the state: the current topology installs all
   three, and ingress, Dex and the tier split are measured in §15–§17.

### 9.1 Metrics backend — partially checked

§8a of the target document recommends VictoriaMetrics over Mimir on grounds of
operational simplicity, and cites `vmselect` reading multiple single-node
instances with automatic deduplication as the multi-cluster fit. **That dedup
claim was attempted and is NOT verified here.**

What was done: two `victoria-metrics` single-node instances (`v1.154.0`) were
run on `:18428` and `:18429`, each with `-vmselectAddr` enabled, and an
identical series was pushed to both. `vmselect` was started against both.

What happened:

- Each single-node instance independently served the pushed series — the write
  path works.
- `vmselect` started and reported healthy.
- **Every documented cluster URL form returned an error.** `/api/v1/query_range`
  → *"unsupported URL format"*. `/select/0:0/<metric>` → HTTP 400,
  *"unsupported path requested"*. Only the bare `/select/0:0/` returned 200.

So the dedup behaviour could not be observed. **It remains sourced from
VictoriaMetrics' own documentation, not from measurement here.** Two practical
notes that *were* learned:

- `/api/v1/import` expects **JSON**; exposition-format ingestion needs
  `/api/v1/import/prometheus`. The former silently returns 204 while dropping
  every sample.
- `-retentionPeriod` cannot be less than one day, which makes short-lived
  experiments awkward.

If the VictoriaMetrics recommendation is pursued, this should be re-verified with
a Kubernetes deployment rather than host binaries.

---

## 10. Summary of required CLI changes

Everything below is currently missing and is a prerequisite for D11.

| # | Change | Where | Risk if omitted |
|---|---|---|---|
| 1 | `experimental.docker.network` pointing at one shared network | `1-cluster/vind-config*.yaml` | No cross-cluster connectivity at all |
| 2 | `networking.podCIDR` unique per cluster | `1-cluster/vind-config*.yaml`, or derived from cluster name | Control plane connects, datapath silently fails |
| 3 | Pre-seed `cilium-ca` into every cluster before its Cilium install | CLI orchestration | No shared trust domain |
| 4 | `cluster.name` / `cluster.id` per cluster | `2-addons/manifests/helm-releases/cilium.yaml` | Not a valid mesh |
| 5 | `authMode: legacy`, `service.type: NodePort`, `method: helm`, `extraDnsNames` | same | Mesh never connects |
| 6 | CoreDNS mesh entries, merged into the existing `hosts` block | `internal/bootstrap/dns.go` | Peers unresolvable |
| 7 | Fix the CoreDNS restart label | `internal/bootstrap/dns.go:42` | Every DNS change silently no-ops |

Items 1 and 2 are one-line changes to the vend values and are prerequisites for
everything else. Item 7 is a standalone bug fix worth doing regardless.

---

## 10a. Fix status

Implemented and verified end-to-end through the product code path.

| # | Change | Status |
|---|---|---|
| 1 | `experimental.docker.network` shared network | **Implemented.** `internal/manifests/vindmesh.go`, opt-in via `platform.mesh.enabled`. |
| 2 | `networking.podCIDR` unique per cluster | **Implemented.** Deterministic from cluster name. |
| 6 | CoreDNS mesh entries in the existing `hosts` block | **Implemented.** `patchCorefileHosts` in `internal/bootstrap/dns.go`. |
| 7 | CoreDNS restart label | **Fixed.** `CoreDNSPodSelector` matches both conventions; restart now fails loudly instead of silently no-opping. |
| — | LoadBalancer disabled in mesh mode | **Added.** Avoids vCluster's macOS `ifconfig` loopback-alias path, which needs `ifconfig` and sudo. |
| 3, 4, 5 | `cilium-ca` pre-seeding, `cluster.name`/`id`, mesh Cilium values | **Not implemented.** These need the cluster-orchestration layer (target decisions D1/D2), which does not exist yet. |

Both clusters are now created **by `manifests.VindMeshValues`** rather than
hand-rolled values, and the full mesh was re-verified against them:

```
shared network: all four containers on shoulders-mesh (10.90.0.2 - 10.90.0.5)
mesha  CIDR=10.39.0.0/24
meshb  CIDR=10.218.0.0/24      <-- distinct, was 10.244.0.0/24 for both

✅ All 2 nodes are connected to all clusters [min:1 / avg:1.0 / max:1]
   meshb: 2/2 configured, 2/2 connected
cross-cluster ping: 3 transmitted, 3 received, 0% loss (~0.50ms)

netpol: other-namespace same-cluster -> BLOCKED (100% loss)
        cross-cluster via k8s:io.cilium.network-name -> ALLOWED (0% loss)
```

**Correction:** the netpol line above is the invalid result corrected in §19.
The remote allowance came from the same-namespace rule, not the cluster label.

### Topology gap found while validating, and closed

The first fix round proved the mesh on **single-node** clusters, because the
harness deleted `experimental.docker.nodes` alongside `ports`. The product's
`small` profile creates **control-plane + 1 worker**, so the validated topology
was not the shipped one.

The harness now keeps `nodes`, and the mesh was re-validated on the real
two-node topology — including probes pinned to the worker so traffic exercises an
intra-cluster hop before crossing the mesh:

```
mesha:  mesha(10.90.0.2), worker-1(10.90.0.3)   CIDR 10.39.0.0/24
meshb:  meshb(10.90.0.4), worker-1(10.90.0.5)   CIDR 10.218.0.0/24

p-mesha  10.39.1.9    worker-1
p-meshb  10.218.2.10  worker-1

worker -> worker across clusters: 0% loss, ~0.40ms   (CP->worker->worker->CP)
netpol on the same 2-node topology: BLOCKED / ALLOWED as expected
```

### Risk R3 resolved: two clusters coexisting

The final validation round stopped stripping host ports entirely, because
per-cluster ports are now rendered from the resolved cluster:

```
mesha  443/tcp=0.0.0.0:443   80/tcp=0.0.0.0:80
meshb  8080/tcp=0.0.0.0:8080 8443/tcp=0.0.0.0:8443
```

Two vind clusters coexisting with distinct ingress was previously impossible.
The mesh still connects (2/2 nodes) and cross-cluster traffic and policy behave
identically.

`TestVindValuesPreservesExistingKeys` now asserts `experimental.docker.nodes`
survives, and `TestVindValuesRendersPerClusterPorts` guards the port rendering.

### Bug found while validating the fix

The first implementation varied the **third** octet (`10.244.231.0/16`). That is
not a valid `/16` network address — it silently normalises to `10.244.0.0/16`,
so **every cluster still received the same range** and the collision persisted.
Unit tests passed; only the live run caught it.

Fixed by varying only the **second** octet (`10.<base+n>.0.0/16`), which is the
only one a `/16` can vary. `ValidatePodCIDR` now rejects any CIDR that is not a
network address, so the mistake cannot return silently:

```
pod CIDR "10.244.231.0/16" is not a network address; it normalises to
10.244.0.0/16. a /16 must have its last two octets set to zero
```

The default base is `16` rather than vCluster's `244` because 244 would leave
only 12 usable `/16`s.



## 11. Incidental findings
1. **§4 of the target document is now partly resolvable without a full test.**
   `1-cluster/vind-config*.yaml` binds host `80`/`443`, so two vind clusters
   cannot coexist. This was hit immediately and is why the harness omits
   `experimental.docker.ports`. Confirmed, not merely inferred.

2. **The profile overlays still build cleanly** (R5, incidental check):

   | Profile | `kubectl kustomize` |
   |---|---|
   | `small` | OK — 9 resources |
   | `medium` | OK — 11 resources |
   | `large` | OK — 11 resources |

   So R5 is not a *correctness* problem today, purely a maintainability one as
   the target document already frames it.

3. **The XRDs are confirmed on the Crossplane v2 API with explicit `scope`** —
   all five use `apiextensions.crossplane.io/v2`, four `Namespaced` and
   `Workspace` `Cluster`. This supports the target document's position that
   spoke-local compositions are already idiomatic v2.

4. **`kube-proxy-replacement` is not set by the repo's `cilium.yaml`** and
   defaults to `false`, though the README claims kube-proxy replacement. Either
   the README overstates the current state or a value is missing from
   `2-addons/manifests/helm-releases/cilium.yaml`. Not investigated; flagged
   because it interacts with the policy tests in §8.1.

5. **Disk.** Started at 51 GiB free, ended at 76 GiB. `docker builder prune -af`
   reclaimed 23 GiB of build cache. Docker **images were deliberately not
   pruned** — vind uses the host Docker daemon as a pull-through image cache, so
   those 24 GiB are load-bearing, not garbage.

6. **Host state left as found.** Both experiment clusters, the shared network and
   the VictoriaMetrics processes were removed. The pre-existing
   `vcluster.cp.shoulders` containers (Exited, 2 weeks old) and the 8 local
   volumes were left untouched.

---

---

## 12. Topology orchestration: first live run

The `up` orchestration was extracted (`cmd/upcluster.go`, `cmd/upcluster_install.go`,
`cmd/upcluster_create.go`) and then run against two live vind clusters through the
real product functions. Stages 1–3 worked first time:

```
stage 1+2+3: create clusters, resolve addresses, seed peer DNS
  e2ehub   -> 10.90.0.2:32379
  e2espoke -> 10.90.0.4:32379
```

Cluster creation, shared-network attachment, address resolution and CoreDNS peer
seeding all behaved as designed. **The Cilium install then failed**, and the
reason is the most important finding in this document.

### 12.0 A circular dependency, and how the install order actually resolves

Two subsequent failures were the same problem seen from different sides, and the
resolution defines the install order.

**A mesh must seed DNS *before* Cilium.** Peers are dialled by DNS name,
because the generated certificates carry DNS SANs rather than IP SANs (§7.2).
So the peer `hosts` entries have to exist before the agents try to connect.

**But CoreDNS cannot be running before Cilium.** A pod with no CNI has no
network, so it cannot be scheduled. Observed directly, repeatedly:

```
NAME                       READY   STATUS    AGE
coredns-6dc6dfcb8f-l6n5x   0/1     Pending   116s
kube-proxy-p6l46           1/1     Running   12m
```

So "wait for CoreDNS, then seed DNS, then install Cilium" deadlocks. The fix is
that **patching the Corefile does not require a running CoreDNS** — CoreDNS reads
its Corefile at startup, so an entry written before the first pod exists is live
the moment that pod starts. `bootstrap.SeedCoreDNSHosts` therefore patches the
ConfigMap and only restarts CoreDNS if a pod happens to be running.

That required distinguishing two cases that used to look identical, and it is
worth stating explicitly because getting it wrong reintroduces R1d:

| Caller | No CoreDNS pod means | Behaviour |
|---|---|---|
| `EnsureClusterDNSWithHosts` (CNI already installed) | the R1d silent no-op | **fails loudly** |
| `SeedCoreDNSHosts` (mesh, pre-CNI) | expected | patches, no restart, succeeds |

Both now share one sentinel, `ErrCoreDNSNotRunning`, and each caller decides.

This is also why the topology install creates **every** cluster before
installing Cilium in **any** of them: peer addresses are Docker-network IPs that
only exist once the peer containers are up.

### 12.1 Helm ignores the context override — install went to the wrong cluster

Symptom: after installing the *hub*, `helm list` showed **no release at all** in
the hub, and the process sat blocked for 7+ minutes (killed with `SIGQUIT` for a
goroutine dump). The release had in fact landed in the **spoke**:

```
NAME    NAMESPACE   STATUS             CHART         APP VERSION
cilium  kube-system  pending-install   cilium-1.19.2 1.19.2
```

Root cause — **two independent cluster-resolution mechanisms that agree in a
single-cluster install and diverge the moment there are two**:

| Mechanism | Resolves the cluster by | In this run it picked |
|---|---|---|
| `internal/kube` helpers | package-level context override | **hub** (`e2ehub`) |
| Helm (`helmcli.EnvSettings`) | ambient kubeconfig current-context | **spoke** (`e2espoke`) |

`EnsureCilium` read the API endpoint through the override (`k8sServiceHost` →
`10.90.0.2`, the hub) but wrote the release to the ambient context (the spoke).
The result is a Cilium in the spoke pointing at the **hub's** API server:

```
cilium-operator: Get "https://10.90.0.2:8443/api/v1/namespaces/kube-system":
  tls: failed to verify certificate: x509: certificate signed by unknown authority
```

and `pending-install` forever, because Helm's `LegacyStrategy` waits on a
DaemonSet that can never become ready.

Why it was invisible until now: in a monocluster install there is only ever one
cluster, so both mechanisms resolve to the same place regardless of disagreement.
**Multi-cluster is the first configuration where this can happen, which makes it
a topology bug rather than an incidental one.**

**Fix:** `CiliumOptions.ContextName` pins `settings.KubeContext`, and a single
`targetClusterContextName()` in `cmd` is now the one place a cluster's context is
derived — used by the kube helpers, the DNS work, and both Helm entry points
(`EnsureCilium`, `UninstallCilium`). The field is documented as required rather
than optional, with the failure mode written down.

### 12.3 A release abandoned in `pending-install` was treated as installed

Third failure, after the hub succeeded and the spoke began installing:

```
spoke cilium: another operation (install/upgrade/rollback) is in progress
```

The cause was a Helm release on the spoke dated **15 minutes earlier**, from the
very first broken run: `cilium-1.19.2`, status `pending-install`. Two separate
issues, one product bug and one harness mistake.

**Product bug.** `releaseExists` tested only whether `helm get` returned a
release, ignoring its status. A release abandoned in `pending-install` by an
aborted install therefore counted as "already installed", so `EnsureCilium`
skipped it — on precisely the cluster that needed the install, leaving a
permanently broken CNI that every later `up` silently ignored. Any retry then
hit Helm's revision lock and failed with the misleading message above.

Fixed by inspecting the release rather than probing for it
(`inspectRelease` → `deployed` / `unfinished` / `absent`). An unfinished release
is now discarded before reinstalling, so `up` is genuinely idempotent *and*
recovers from a failed run instead of wedging.

**Harness mistake, worth recording because it is easy to hit.** vCluster keeps
its API server state in Docker **volumes** (`vcluster.cp.<name>.{bin,cni-bin,etc,var}`),
so `docker rm -f` on the containers leaves the cluster's `kube-system` intact. A
recreated container came back with a 15-minute-old Cilium release and looked
brand new. Cleaning the experiment requires removing the volumes too.

This is **not** a product defect: `DeleteVindCluster` already force-removes the
containers and all four volume suffixes precisely for this reason, with a
comment saying so. Only the throwaway harness was cleaning up incompletely.

### 12.4 The peer address was malformed — a doubled port

With both clusters installed and every pod healthy, the mesh still reported
`0/2 connected`, symmetrically from both sides:

```
❌ cilium-zjpmr is not connected to cluster e2espoke:
   remote cluster configuration required but not found
   💡 This is likely caused by KVStoreMesh not being connected to the given cluster
```

The kvstoremesh log named the cause in a single line:

```
endpoints=[https://10.90.0.2:32379:32379] clusterName=e2ehub
```

**The port is doubled.** The orchestration was putting `IP:port` in the
`address` field while also setting `port: 32379`, and Cilium concatenates them.
The peer was therefore dialled at a port that does not exist, so KVStoreMesh
never connected, and the remote cluster configuration was never retrievable.

The fix is to use the form that was **already proven** in §7.2 — a bare DNS name
with the port as a separate field:

```yaml
clustermesh:
  config:
    clusters:
      e2espoke: { address: e2espoke.mesh.cilium.io, port: 32379 }
```

Worth noting what this cost, because it is a lesson about the process rather than
about Cilium: **the proven values were already written down in §7.2 of this same
document, and the implementation did not follow them.** The IP form looked more
direct — it is what CoreDNS resolves to, and it avoids a DNS dependency — so it
was chosen without going back to the evidence. The measurement existed; it just
was not consulted.

A secondary correction from the same logs: the *agent* established its etcd
connection with the malformed endpoint and reported success, while only
kvstoremesh failed. So "the agent connected" is not sufficient evidence that a
mesh is formed — `cilium clustermesh status` counting **KVStoreMesh** replicas is
the check that catches this.

### 12.5 CoreDNS was crash-looping — records were inserted after a directive

With the peer address corrected, the endpoint was right
(`https://e2ehub.mesh.cilium.io:32379`) but still timed out. This time the
cause was upstream of Cilium entirely: **CoreDNS was not running in either
cluster.**

```
nslookup: write to '10.99.14.164': Connection refused
;; connection timed out; no servers could be reached
```

while a direct ping to the peer address worked fine:

```
--- 10.90.0.2 ping statistics ---
2 packets transmitted, 2 packets received, 0% packet loss
```

So cross-cluster IP routing was healthy and only *name resolution* was broken.
The CoreDNS logs named the defect:

```
plugin/hosts: /etc/coredns/Corefile:14 - Error during parsing: unknown property '10.90.0.4'
```

**`patchCorefileHosts` appended records at the end of the hosts block** — after
the `fallthrough` directive. The hosts plugin's grammar is records first,
directives last:

```
hosts [FILE] [ZONES...] {
    [INLINE]          <- records
    fallthrough [ZONES...]
    reload DURATION
    ttl SECONDS
}
```

Anything after the first directive is parsed as a directive *property*, so the
plugin refuses to start. This is a **crash loop, not a degraded lookup** — the
cluster loses DNS entirely, which is why mesh peers became unresolvable and the
error surfaced as a KVStoreMesh connection timeout several layers away.

Fixed by inserting records immediately after the block's opening line, so they
always precede the directives. The regression test models the plugin's grammar
rather than string-matching, and was verified to **fail against the old
insertion point and pass against the new one** — a test that cannot fail is
worth nothing:

```
--- with OLD insertion point (expect FAIL)
    dns_test.go:454: Corefile line 10 ("10.90.0.4 e2espoke.mesh.cilium.io") is not valid inside the hosts block
--- restored (expect PASS)
ok
```

This is the fourth instance of the same underlying failure mode in this section:
a configuration value looked correct in isolation and only failed in
combination. Every one of them was caught by running the real thing.

### 12.7 The topology install works end to end

With the peer address corrected and CoreDNS parsing, the full sequence
completed and the mesh formed:

```
=== cluster 1/2: e2ehub (hub) ===
    stage 1+2+3: create clusters, resolve addresses, seed peer DNS
      e2ehub   -> 10.90.0.2:32379
      e2espoke -> 10.90.0.4:32379
    harvested the hub Cilium trust anchor
    Seeded the shared Cilium trust anchor into e2espoke
=== cluster 2/2: e2espoke (spoke) ===
✅ All 2 nodes are connected to all clusters [min:1 / avg:1.0 / max:1]
✅ All 1 KVStoreMesh replicas are connected to all clusters [min:1 / avg:1.0 / max:1]
🔌 Cluster Connections:
  - e2espoke: 2/2 configured, 2/2 connected - KVStoreMesh: 1/1 configured, 1/1 connected
```

and cross-cluster pod-to-pod traffic works **in both directions**:

```
e2ehub probe at 10.187.0.72
e2espoke probe at 10.187.0.188
ping e2ehub   -> e2espoke: 3 packets transmitted, 3 received, 0% packet loss
ping e2espoke -> e2ehub:   3 packets transmitted, 3 received, 0% packet loss
```

The distinct pod CIDRs per cluster are visible in the nodes, confirming R1c's
second fix held:

| Cluster | Node pod CIDRs |
|---|---|
| `e2ehub` | `10.16.0.0/24`, `10.16.1.0/24` |
| `e2espoke` | `10.187.0.0/24`, `10.187.2.0/24` |

And the peer configuration was applied in exactly the §7.2 form:

```yaml
clustermesh:
  config:
    clusters:
      e2espoke:
        address: e2espoke.mesh.cilium.io
        port: 32379
```

**This closes R1a, R1b and R1c for the CLI-only install path.** What remains
untested is the same flow with Flux installed, which R8 makes unsafe to attempt
until the Cilium ownership collision is resolved.

Test: `SHOULDERS_MESH_E2E=1 go test ./cmd/ -run TestTopologyOrchestrationE2E`
(258s, needs Docker). It exercises the real product functions — no test-only
install path — and asserts both mesh formation and bidirectional connectivity.

### 12.8 Flux reconciles Cilium too — and would erase the mesh

Independent of 12.1, the same run exposed a **pre-existing ownership collision**
that blocks ClusterMesh on the real product path.

`2-addons/flux/kustomizations.yaml` reconciles the path
`./2-addons/manifests/helm-releases`, whose kustomization includes
`cilium.yaml` — a `HelmRelease` named `cilium`, in `kube-system`, pinned to
**1.20.2**. That is the **same release name in the same namespace** the CLI
installs with Helm. `helm-controller` and the CLI both drive the same Helm
release secret, so on the first Flux sync (interval `10m`) Flux takes over.

Three consequences, in increasing severity:

1. **Version drift.** `internal/config/types.go` pins
   `DefaultCiliumVersion = "1.19.2"`; Renovate has moved the manifest to
   `1.20.2`. Flux silently upgrades every cluster. The CLI's pin is decorative.
2. **Values divergence.** The CLI passes `kubeProxyReplacement: true`,
   `k8sServiceHost`/`k8sServicePort` and `extraConfig.api-rate-limit`. The
   manifest's `values:` block has **none of the three** — it only repeats
   `ipam`, `gatewayAPI`, `prometheus`, `dashboards`, `hubble` and `envoy`.
3. **The mesh config would be wiped.** Everything added for ClusterMesh
   (`cluster.name`/`id`, the whole `clustermesh` block, `routingMode`, `protocol`)
   exists only in the CLI's values. A Flux reconcile replaces them with the
   manifest's set, and every mesh participant drops out of the mesh.

Note this also retroactively explains **incidental finding 4**: the manifest
omits `kubeProxyReplacement`, which is why that finding was open. It is not a
documentation error — the manifest is genuinely missing the value.

**Why the manifest should not own Cilium at all**, independent of versions:
Cilium is the CNI, and Flux cannot reconcile *anything* until the CNI is up. The
bootstrap dependency runs in the opposite direction, so the same Flux instance
can never legitimately own Cilium's lifecycle. Cilium is bootstrap-layer;
`2-addons/manifests/helm-releases` is platform-layer. The overlap is a layering
error, not a duplication that happens to be convenient.

Status: **diagnosed, not yet fixed.** Fixing it touches monocluster behaviour,
so it is raised as **R8** in the target document rather than changed unilaterally.

---

---

## 13. Cilium hand-off to Flux — PROVEN

The central architectural claim: **the CLI bootstraps Cilium, then Flux adopts
it** (see [fluxcd/flux2#3070](https://github.com/fluxcd/flux2/discussions/3070)).
Tested against the repository's own `2-addons/manifests/helm-releases/cilium.yaml`,
applied verbatim — a paraphrase would not have tested the real contract.

```
--- PASS: TestCiliumHandOffToFlux (105.87s)
condition Ready=True:  UpgradeSucceeded  Helm upgrade succeeded for release
                                  kube-system/cilium.v2 with chart cilium@1.20.2
HelmRelease history: 1.20.2 rev2 deployed
```

Flux took over the CLI's release (rev 2, not a second install), and everything the
CLI discovered survived:

```
kubeProxyReplacement: true
k8sServiceHost: 10.90.0.2
k8sServicePort: 8443
cluster:
  id: 35
  name: handoff
clustermesh: …
protocol: vxlan

Cilium:       OK
ClusterMesh:  OK
```

### 13.1 A latent ordering hazard the hand-off exposed

The first attempt failed, and not for a reason related to the mesh:

```
execution error at (cilium/templates/validate.yaml:79:13):
  Service Monitor requires monitoring.coreos.com/v1 CRDs
```

`cilium.yaml` set `prometheus.serviceMonitor.enabled: true`, but the
`monitoring.coreos.com` CRDs come from the **kube-prometheus-stack** HelmRelease in
the same kustomization — not from `2-addons/manifests/crds`, which only carries
`gateway-api.yaml`.

This cannot be fixed with `dependsOn`, because the dependency is genuinely
circular: kube-prometheus-stack's pods need the CNI to schedule, so it cannot
precede Cilium.

The failure is **transient rather than fatal** — flux retries on `interval: 5m`
and succeeds once kube-prometheus-stack lands. Observed retrying every ~7s while
the CRDs were absent:

```
Warning  UpgradeFailed  helmrelease/cilium  Helm upgrade failed … Service Monitor requires …
Warning  UpgradeFailed  helmrelease/cilium  Helm upgrade failed … Service Monitor requires …
Warning  UpgradeFailed  helmrelease/cilium  Helm upgrade failed … Service Monitor requires …
```

Which is precisely why it went unnoticed: a permanently-noisy, permanently-
self-healing failure. It is also the same reason the **CLI** sets
`serviceMonitor.enabled: false` — the chart's own escape hatch,
`trustCRDsExist: true`, would only move the failure to object creation.

Fixed by setting `serviceMonitor.enabled: false` in `cilium.yaml`, matching the
CLI, with the reasoning recorded inline so it is not "corrected" back later.

### 13.2 Merge order, corrected

An earlier draft of this work claimed `valuesFrom` takes precedence over inline
values. The [Flux HelmRelease API](https://fluxcd.io/flux/components/helm/helmreleases/#optional-values)
says the opposite:

> The values are merged in the order given, with the later values overwriting
> earlier, and then **inline values overwriting those**.

So an overlapping key in git would silently win over what the CLI discovered at
runtime. The design was changed as a result: the two key sets are kept
**disjoint**, which removes the precedence question instead of depending on an
answer. `TestGitOpsCiliumManifestReferencesTheConfigMap` now fails if any
CLI-owned key appears in the manifest's inline values, so the invariant is
enforced rather than merely documented.

`optional: true` is verified against the same API — a missing ConfigMap is
ignored, so the documented non-CLI path (`kubectl apply -k
2-addons/profiles/<profile>/flux`) keeps working with no CLI to write it.

### 13.3 The profile conflict — RESOLVED

`dashboards.enabled` and `hubble.{relay,ui}` were profile-dependent in the CLI
(`EnableObservability`, `EnableHubble`) and set to `true` in git. By the merge
rule above, git wins, so a `small` profile cluster got hubble and Cilium
dashboards its profile was meant to omit.

**Resolved by moving the profile's decision into git**, which is where the
profile mechanism already lives for every other addon: the base
`2-addons/manifests/helm-releases/cilium.yaml` *is* the medium profile, and
`2-addons/profiles/{small,large}/helm-releases/cilium.yaml` patch the
observability on or off. The CLI no longer sets those keys at all, and
`ProfileSpec.HubbleUI` / `ProfileSpec.CiliumObservability` are gone.

Two tests hold the arrangement in place:

- `TestCliDoesNotSetProfileDerivedCiliumValues` — the CLI must not set them.
- `TestProfileManifestsDecideCiliumObservability` — the rendered manifests must
  express each profile's intent (small off, medium and large on).
- `TestCliAndGitOpsAgreeOnStaticCiliumValues` — the static keys the two sides
  share must agree, because flux merges the manifest's inline values over the
  CLI's and a disagreement is a silent reconfiguration of a running CNI.

Test: `SHOULDERS_CILIUM_HANDOFF=1 go test ./cmd/ -run TestCiliumHandOffToFlux`
(~2 min, needs Docker).

---

## 14. Flux never created the Cilium HelmRelease — SOLVED

Found by a full monocluster `shoulders up --local` regression run, and
**pre-existing** — it reproduces with the unmodified `cilium.yaml` from HEAD.

### 14.1 The symptom

A `small`-profile install completes successfully (4:15, all 8 other
HelmReleases Ready) and the cluster is healthy — but there is no
`HelmRelease/cilium` anywhere:

```
$ kubectl get helmrelease -A
cnpg-system/cnpg   dex/dex   garage/garage   headlamp/headlamp
kyverno/kyverno   kyverno/kyverno-policies
observability/kube-prometheus-stack
crossplane-system/crossplane                       # 8 total, no cilium
```

The CNI still works, because the **CLI** installed it. So the cluster looks
completely fine and the gap is invisible.

### 14.2 It looked like a kustomize implementation difference

`kubectl kustomize` and flux's own builder disagree on the same input:

| Renderer | Path | Objects | cilium |
|---|---|---|---|
| `kubectl kustomize` | `2-addons/profiles/small/helm-releases` | 13 | ✅ |
| `flux build kustomization helm-releases` | same | 12 | ❌ |

Flux renders the artifact it actually stored (verified by pulling the OCI layer
out of the in-cluster registry — the snapshot does contain `cilium.yaml`, with
this worktree's changes, and its profile kustomization does not delete it). The
kustomize-controller's own log agrees, listing 12 applied objects and never
mentioning cilium.

Bisection so far:

- Not the comment block (stripped every comment → still dropped).
- Not this worktree's changes (HEAD's `cilium.yaml` → still dropped).
- Not the API server: applying that exact file with `kubectl apply` succeeds, and
  helm-controller then adopts the CLI's release cleanly — the hand-off itself
  works perfectly once the object exists.
- Not pruning: the object survives a forced reconcile rather than being removed.

So the defect is confined to **flux's kustomize builder dropping one resource
from a 13-resource build**. The trigger inside the file is not yet identified.

### 14.3 Why this matters more than it looks

It cuts both ways, and the honest reading is that it is *currently masking* R8:

- Good: the mesh config cannot be erased by Flux, because Flux never manages
  Cilium. The R8 risk was real but had not been firing.
- Bad: **the hand-off does not happen automatically**, so GitOps is not actually
  managing the CNI — the bootstrap-only state is the steady state, and nobody
  would notice because the cluster is healthy.

It also means §13's proof is of the *hand-off mechanism* (validated by applying
the real manifest and reconciling) rather than of an end-to-end `shoulders up`.
Those are different claims and the docs keep them separate.

### 14.4 SOLVED — it was not a kustomize bug at all

The bisection above exonerated the kustomize library (flux uses the same
`v0.21.1` as kubectl), the snapshot, the source materialisation, and the
postBuild substitution — every one of them produced **13** objects including
cilium.

The answer came from reading the live Kustomization's spec instead of assuming
what was in it:

```json
"patches": [{
  "patch": "apiVersion: helm.toolkit.fluxcd.io/v2\nkind: HelmRelease\nmetadata:\n  name: cilium\n  namespace: kube-system\n$patch: delete\n",
  "target": {"kind": "HelmRelease", "name": "cilium", "namespace": "kube-system"}
}]
```

**The CLI deliberately deletes the Cilium HelmRelease from Flux's set.**

`SuspendCiliumHelmRelease` in `internal/bootstrap/cilium.go` patched the Flux
`helm-releases` Kustomization to add exactly that delete patch, and `up` called
it unconditionally:

```go
// Always suspend the Flux-managed Cilium HelmRelease. When Cilium is
// enabled, the CLI manages the installation directly; when it is
// disabled, this keeps Flux from installing it implicitly.
```

`git log -S` dates it to **c3fc83b "Replace kind with vind"** — a
kind-to-vind *migration* workaround, whose stated reason was that the Git
manifest still carried values targeting a kind cluster. It was never meant to
be the permanent architecture, and it outlived its reason.

So there was no bug. There was a **deliberate exclusion that had become
permanent**, and it had three consequences:

1. GitOps was the *nominal* owner of the CNI while never reconciling it. The
   `cilium.yaml` in `2-addons/manifests/helm-releases/` was dead weight —
   always deleted at runtime.
2. **Renovate's Cilium version bumps never reached a running cluster.** The
   version drift I had attributed to a stale CLI constant was the visible
   symptom of this: the manifest was never applied, so `1.20.2` in git meant
   nothing.
3. The hand-off could not happen at all, which is what made the "Flux would
   erase the mesh" risk moot.

### 14.5 The fix

`SuspendCiliumHelmRelease` and `PatchCiliumHelmRelease` are gone, replaced by
`SetCiliumHelmReleaseFluxOwnership(kubeconfig, fluxOwns bool)`, which is
idempotent in both directions and encodes the *actual* rule:

- **Cilium enabled** → Flux owns it. The CLI bootstraps the install (Flux needs
  a CNI before it can reconcile anything) and Flux adopts that release.
- **Cilium disabled** (the default for `provider: existing`) → the cluster has
  its own CNI, and the unconditional `cilium.yaml` must not install one. The
  delete patch is applied in this case only.

Verified end to end on a real `shoulders up --local`:

```
$ kubectl -n kube-system get helmrelease cilium
cilium   2m26s   True   Helm upgrade succeeded for release kube-system/cilium.v2
                        with chart cilium@1.20.2

$ kubectl -n flux-system get kustomization helm-releases -o jsonpath='{.spec.patches}'
[]                                    # Flux owns Cilium

$ helm get values cilium -a | grep -E '^kubeProxyReplacement|^k8sServiceHost'
k8sServiceHost: 172.19.0.2
kubeProxyReplacement: true

$ helm list -n kube-system
cilium  kube-system  2  deployed  cilium-1.20.2  1.20.2
```

The CLI installed `1.20.2` and Flux adopted it at `1.20.2` — **rev 2, not a
surprise upgrade**. `UpgradeFailed` events: **0**, so the `serviceMonitor` CRD
ordering hazard is gone too.

### 14.6 What this changes about the risk register

R8 was recorded as "Flux reconciles Cilium and would erase the mesh". That
framing was wrong twice over: Flux was *supposed* to reconcile Cilium, and it
was being prevented from doing so. The risk was not that Flux would take over
— it was that it never would.

---

## 15. The tier split, end to end

D3 puts the platform tier in the hub and the workload tier in the spokes. That
is now implemented and **measured on a real two-cluster topology**, installed
from scratch with the whole platform on both.

```
$ shoulders up --config topology.yaml --local
  ✓ Create vind cluster
  ✓ Install Cilium CNI
  ✓ Install Flux CD
  ✓ Reconcile Flux kustomizations
  ✓ Wait for platform deployments
  ✓ Configure gateway routes
  ✓ Validate cluster status
  Shoulders platform provisioned in 02:10
```

### 15.1 What each cluster ended up with

| | hub | spokea |
|---|---|---|
| HelmReleases | cnpg, crossplane, dex, garage, headlamp, **cilium**, kyverno, kyverno-policies, kube-prometheus-stack | cnpg, crossplane, garage, **cilium** |
| namespaces | cnpg-system, crossplane-system, **dex**, garage, **headlamp**, **kyverno**, **observability** | cnpg-system, crossplane-system, garage |
| pods healthy | 39/39 | 26/26 |
| `shoulders status` | All systems healthy | All systems healthy |

The spoke has **no** dex, headlamp, kyverno, observability, policy-reporter,
trivy, falco, alloy, loki or tempo — and no namespaces for them.

### 15.2 The mesh, and traffic across it

```
hub:    All 2 nodes are connected to all clusters    All 1 KVStoreMesh replicas are connected
spokea: All 2 nodes are connected to all clusters    All 1 KVStoreMesh replicas are connected
```

```
hub -> spoke:   3 packets transmitted, 3 received, 0% packet loss
spoke -> hub:   3 packets transmitted, 3 received, 0% packet loss
```

### 15.3 How the split is expressed, and why

As **delete patches and value overrides on the Flux Kustomizations the CLI
applies** — not as a different manifest path.

A path would have to be per cluster, and therefore tied to the cluster names
the operator chose. D6 anticipated `pathPrefix: clusters/<name>`, but the names
are the operator's to pick, so a default would break any topology that did not
happen to match the repository. Patches are profile-independent and work for
any cluster name.

The patches travel in the object the CLI applies, not in a patch applied
afterwards, so Flux cannot reconcile the platform tier in the window between
creating a Kustomization and excluding it.

### 15.4 Five defects the first live topology run found

Every one of them was invisible in a single-cluster install, and every one was
found by running the real thing.

1. **The tier was lost on the `--local` path.** `prepareLocalOCISource` rebuilt
   the Flux source from the file-level config, dropping the tier — so a spoke
   reconciled the platform tier even though the CLI had been told otherwise.
   The source now carries the tier.
2. **A removed Kustomization left a dangling dependency.**
   `helm-releases` depended on `headlamp`, which a spoke does not create:
   `dependency 'flux-system/headlamp' not found`. That blocks the whole tier,
   not just the dependent Kustomization. Dependencies on removed Kustomizations
   are now pruned.
3. **A resource in an excluded namespace.** The `dex-tls` Secret is built by
   the helm-releases Kustomization but lives in the `dex` namespace, which the
   spoke does not create:
   `Secret/dex/dex-tls not found: namespaces "dex" not found`.
4. **An unsatisfiable HelmRelease dependency.** CNPG waits on
   `kube-prometheus-stack` for the monitoring CRDs. On a spoke that release does
   not exist, so CNPG never installed at all:
   `unable to get 'observability/kube-prometheus-stack' dependency`. The
   dependency is dropped on a spoke.
5. **A chart that needs a CRD the spoke does not have.** With the dependency
   gone, CNPG installed and then failed:
   `no matches for kind "PodMonitor" in version "monitoring.coreos.com/v1"`.
   A PodMonitor is a scrape configuration — there is no Prometheus on a spoke to
   scrape it — so `monitoring.podMonitorEnabled` is set to false there.

The last two are the same class of problem: **the spoke does not have the
platform, and something in the workload tier quietly assumed it did.**

### 15.5 The guards that stop them coming back

- `TestTierExclusionsCoverEveryPlatformManifest` reads the manifests
  themselves and fails if a resource is neither kept by the workload tier nor
  excluded from it. Adding an addon to the platform tier without an exclusion
  fails here rather than in a cluster.
- `TestWorkloadTierDropsDependenciesOnRemovedKustomizations` and
  `TestWorkloadTierDependencyDropsAreComplete` cover the dependency pruning and
  assert that the dropped `dependsOn` lists are the *whole* list, which is what
  makes dropping them safe.
- `TestWorkloadTierPatchesAreValidYAML` parses every patch and checks its
  target matches its body, so a malformed patch fails as a configuration error
  rather than as a flux reconciliation error.
- `TestWorkloadTierWaitsForNothingPlatform` covers the wait list: a spoke used
  to wait for `dex/dex`, which never appears, so the install hung on
  "Wait for platform deployments" for the full health-check timeout.

### 15.6 What is still missing

> **Superseded in part.** The first two bullets below were accurate when this
> section was written and are not any more: the spoke's Headlamp is measured in
> §16, and spoke telemetry in §17. The domain bullet still holds.

- **The spoke has no Headlamp.** The design gives each spoke its own, but it
  would have to verify the hub's Dex issuer, which needs the hub's Dex CA
  certificate — cross-cluster secret distribution, the same subsystem **D4**
  defers. Shipping a Headlamp with a broken login would be worse, so it is
  excluded. The hub's Headlamp is the topology's control-plane surface.
- **D13 — telemetry to the hub.** Alloy runs in the hub only. A spoke's Alloy
  needs the hub's OTLP endpoints, which is the same cross-cluster reachability
  question, and is not implemented.
- **`platform.domain` is hub-only, by decision.** The hub declares it; a spoke
  declares none and serves no platform hostnames. The validation requires the
  hub to declare one in a topology, and rejects two clusters declaring the same
  one. See R3.

---

## 16. The spoke's Headlamp: measured, not assumed

§15.6 said the spoke's Headlamp was blocked on "cross-cluster secret
distribution — the same subsystem D4 defers". **That was wrong**, and the
measurements below show why.

### 16.1 A CA certificate is not a secret

The claim rested on the spoke's Headlamp needing the hub's Dex CA. But a CA
*certificate* is public material — it is what a certificate authority
publishes. Copying one from the hub to the spoke is not credential distribution
in the sense D4 was about (Kafka credentials, database passwords).

And it turned out no copy was needed at all: the CLI generates the Dex CA
itself, so it only has to **use the same material for every cluster** rather
than copying it between them. The Dex TLS material is now generated once per
topology (`topologyPlan.hubTLS`) and shared.

### 16.2 Reaching the hub's issuer from a spoke

Measured, from a spoke pod:

| Path | Result |
|---|---|
| `https://dex.<domain>` via the host's loopback | **fails** — a pod's 127.0.0.1 is its own |
| the hub's dex ClusterIP | **fails** — plain ClusterIPs do not cross clusters |
| the hub's dex pod IP | **fails** — the dex pod does not serve TLS on 443 |
| **the hub's shared-network address** | **works** — `10.90.0.2:443`, 0% loss |

The last one is the answer, and the CLI already computed that address for the
mesh. So the spoke's CoreDNS is seeded with the hub's issuer:

```
$ kubectl --context spokea -n probe exec pr -- getent hosts dex.hub.127.0.0.1.sslip.io
10.90.0.2         dex.hub.127.0.0.1.sslip.io
```

and the OIDC discovery document is served over the mesh, with the shared CA:

```
$ curl --cacert ca.pem https://dex.hub.127.0.0.1.sslip.io/.well-known/openid-configuration
{
  "issuer": "https://dex.hub.127.0.0.1.sslip.io",
  "authorization_endpoint": "https://dex.hub.127.0.0.1.sslip.io/auth",
  ...
```

### 16.3 Dex matches redirect URIs exactly

The first attempt used a wildcard redirect URI (`http://headlamp-*`) so one
client would cover every spoke.

**That does not work.** Dex rejected every callback it should have matched:

```
level=ERROR msg="unregistered redirect_uri" redirect_uri=http://headlamp-spokea...:8080/oidc-callback client_id=kubernetes
```

An earlier check of this had looked like a pass, because the grep pattern
missed the message. Dex's `staticClients` matching is exact, so **each spoke
needs its own client**, registered with its own callback URI.

### 16.4 Registering the client

The hub's dex runs with `storage: {type: memory}`, so it never reads the
Kubernetes CRDs — the `OAuth2Client` route does not apply without moving dex's
whole configuration out of the HelmRelease. The gRPC `CreateClient` API needs a
mutually trusted client certificate and an endpoint the chart does not expose.

What works is patching the hub's dex HelmRelease through the Flux Kustomization
that owns it — the same mechanism the tier exclusions use. The patch replaces
the `staticClients` list, because kustomize has no merge key for it, so the
list is read from the live release and written back with the new client added.

Measured, from a spoke:

```
$ curl -L --cacert ca.pem "https://dex.../auth?client_id=headlamp-spokea&redirect_uri=http://headlamp-spokea...:8080/oidc-callback&..."
Log in to Your Account          <- the hub's dex accepted it

$ curl -L --cacert ca.pem "https://dex.../auth?client_id=headlamp-spokea&redirect_uri=http://evil.example.com/oidc-callback&..."
Unregistered redirect_uri        <- and only its own callback
```

The hub ends up with all three clients, and the spoke's Headlamp works:

```
hub:    grafana, kubernetes, headlamp-spokea, cnpg, crossplane, dex, garage, headlamp, cilium, kyverno, ...
spokea: cnpg, crossplane, garage, headlamp, cilium
```

### 16.5 A bug this found, and what it cost

The registration read the client list with `unstructured.NestedMap`, which
**errors on a list**. The error was treated as "no clients", so the patch
replaced the whole list with just the new one — **silently removing the hub's
own Grafana and Headlamp clients**. The spoke registered; the hub's Headlamp
stopped working, and nothing said so.

`TestDexStaticClientsReadsAList` now covers it, and
`TestWithClientReplacesOnlyTheSameID` covers the replacement rule.

### 16.6 The hub's Headlamp is unchanged

The hub keeps the client it always had (`kubernetes` / `headlamp-dex-secret`),
so a monocluster and a hub are unaffected by any of this. The wildcard
redirect URI that was added to `dex.yaml` has been removed, since dex does not
support it and it was misleading.

### 16.7 What this cost in defects

Three more, all found by running the thing:

- The tier was lost on the `--local` path (§15.4).
- The spoke's Headlamp waited on `dex`, which a spoke does not have — the same
  class as CNPG waiting on the prometheus stack.
- The Dex TLS material was regenerated per cluster, so the hub's certificate
  did not verify against the CA the spoke was given. The plan now owns it.

---

## 17. Phase 0: a tenant on a spoke

Phase 0's premise was that **nothing had ever run on a spoke**. Everything
below is what it took to make one example tenant run there — through the real
delivery path, in the laptop topology, not in a unit test.

The tenant is `3-user-space/`: a `Workspace` named `team-a`, a web app, a CNPG
state store and a scheduled curl. It is deliberately boring. It is also the
first thing in the repository that a *user*, rather than the platform, would
deploy.

### 17.1 The delivery path did not exist, in three independent ways

Each was enough to stop it on its own, and none of them reported itself as the
same problem.

| Gap | What it looked like |
|---|---|
| No Flux Kustomization pointed at `3-user-space/` | nothing happened; the manifests were never read |
| The Flux list is Go, not git | adding it to `2-addons/flux/kustomizations.yaml` changed nothing |
| The local OCI snapshot packed `2-addons/` only | `kustomization path not found: stat /tmp/kustomization-.../3-user-space/workspaces` |

The second deserves stating plainly: `2-addons/flux/kustomizations.yaml` reads
like the source of truth and is not. `fluxKustomizationsForProfile` in the CLI
builds the set flux actually receives — that is where tier filtering,
dependency pruning and profile selection happen. Editing the YAML is
documentation, not delivery.

The third is the interesting one. `BuildSnapshotWithOverlay` walked `2-addons/`
and wrote tar names relative to the repository root, so `./2-addons/...`
resolved exactly as it does from a GitRepository. That was correct for every
Kustomization that existed when it was written. The snapshot then built
successfully, pushed successfully, and flux reported a path that was not in the
artifact — a whole failure class with **no error on the producing side**.

`ManifestRoots` is now `{2-addons, 3-user-space}`; names stay
repo-root-relative and a missing root is skipped, so staged (airgap) trees
still build. `TestBuildSnapshotIncludesUserSpace` asserts the tenant path is in
the artifact.

### 17.2 Admission control belongs to both tiers (D16)

Reading `workspace-composition.yaml` answered "does a spoke need Kyverno?" more
decisively than any amount of reasoning about policy architecture had:

```
- name: kyverno-policy
  ...
  kind: ClusterPolicy
```

A `Workspace` emits a Kyverno `ClusterPolicy` as part of its composition. On a
spoke without the CRD or the engine, that resource never converges, so the
composition never becomes ready — the **first** `Workspace` a tenant creates is
enough to strand the whole user-space path. Kyverno is therefore no longer
platform-only: it is shared, installed in both tiers, and the workload tier no
longer excludes it. The hub keeps its own engine, because the hub's platform
pods are admitted by the hub's API server.

The cost is four ServiceMonitors and the Grafana dashboard disabled on a spoke
(there is no Prometheus there to scrape an admission controller yet — D19 is
about metrics, not admission).

The policy *source* stays in git. What is tier-specific is the engine.

### 17.3 The example could not have run as written

Four defects, all present before the first pod was scheduled:

- **`nginx:latest` with `runAsNonRoot: true`.** The stock image starts as root,
  so the kubelet refuses the container.
- **Probes on `/ready` and `/healthz`.** Neither path exists in the sample
  server; liveness would have killed the pod, then looped.
- **`readOnlyRootFilesystem: true` with no writable `/tmp`.** nginx writes there
  at start-up.
- **A `team-a-app-config` secret that nothing creates.** Containers failed with
  `CreateContainerConfigError` before any application code ran.

The image is now `nginxinc/nginx-unprivileged:1.27-alpine`, probes point at
`/`, `/tmp` is an `emptyDir`, and the secret reference is `optional: true`
until SOPS/age lands. That last one is a deliberate temporary weakening, and
the reason Phase 3 is still on the list rather than "done".

### 17.4 Two tier-specific failures

- **A namespace the tier excluded but still needed.** `Namespace/observability`
  was removed by the platform tier while the workload tier had just gained two
  releases *in it*:
  `HelmRelease/observability/alloy-agent not found: namespaces "observability" not found`.
  The namespace is now kept by the workload tier: the spoke's telemetry agent
  lives there. This is the same class as §15.4's dex-tls Secret — the split
  removes a namespace and something else quietly depends on it.
- **Alloy's `targets` is not a list of strings.** `targets = ["host:port"]`
  fails with `conversion from 'string' is not supported` and crash-loops. The
  agent needs `[{ "__address__" = "..." }]`.
- **A listener whose certificate the tier removes.** The Gateway declares a
  `dex-https` listener on 443 with `certificateRefs: kube-system/dex-tls`. A
  spoke excludes the platform tier, which is where that Secret lives, so the
  listener could never resolve:

  ```
  dex-https  ResolvedRefs=False  InvalidCertificateRef:
             Invalid CertificateRef, Secret "dex-tls" not found
  ```

  and the published 443 port accepted a TCP connection and then dropped every
  TLS handshake (`SSL_ERROR_SYSCALL`) — a dead port that looked open. The
  listener is hub-only; the workload tier removes it with a JSON6902 patch that
  `test`s the listener name before `remove`-ing it, so a reordering fails the
  build instead of removing the HTTP listener.

### 17.5 Three defects that only appear on a second run

`down`/`up` has to work, and idempotent `up` is the harder half.

- **A server-side-apply conflict.** The CLI installs Cilium with Helm before
  handing it to flux. On a re-run the release exists and is owned by
  `helm-controller`, so the API server rejects the CLI's apply:
  `clustermesh-apiserver-admin-cert ... conflicts with "helm-controller"`.
  `fluxOwnsCiliumRelease()` now skips the Helm action when
  `HelmRelease/kube-system/cilium` exists, while still publishing the runtime
  values that flux merges.
- **A published port that mapped to nothing.** A spoke publishes 8080/8443 on
  the Docker host, but the values rendered `8080:8080` and `8443:8443`. Cilium's
  Gateway runs Envoy with `hostNetwork` on the node and binds its listener
  ports — 80 and 443 — in *every* cluster. The host forwarded 8080 to a
  container port where nothing listened: **000 from the host, 200 from inside
  the cluster**. The mapping is now `host:80` / `host:443`; monocluster renders
  `80:80` and `443:443` exactly as before.
- **A non-numeric user.** `curlimages/curl` declares `USER curl_user`, and the
  kubelet cannot check a name against `runAsNonRoot`:
  `container has runAsNonRoot and image has non-numeric user (curl_user)`.
  The example names the uid (`100`), which makes the check decidable.

### 17.6 D13, measured

D13 said a spoke should push telemetry to the hub rather than have the hub
scrape it. Measured on the live pair, with the spoke's `alloy-agent` remote-
writing to the hub:

| Check | Result |
|---|---|
| hub → spoke DNS for the hub's Prometheus | `prometheus.hub.127.0.0.1.sslip.io → 172.19.0.2` |
| the hub's remote-write endpoint from the spoke | `/-/ready` → **200** |
| `count by (cluster) (kube_pod_info)` on the hub | `{cluster="spokea"} 41`, plus 39 samples with no cluster label (the hub) |

So 41 of the spoke's series are in the hub's Prometheus, labelled by cluster, and
the hub's own series are not labelled at all — `externalLabels` apply to
federation and alerting, not to locally stored samples. **An empty `cluster`
label means "hub"**; that is the convention, and it is the cheapest one that
does not require rewriting the hub's whole storage path.

### 17.7 What this run proves

The tenant, delivered end to end by flux on the spoke:

- `Workspace/team-a` **Ready**, `WebApplication/team-a-instance` **Ready**,
  `StateStore/team-a-db` **Ready** (CNPG healthy), `Workload/team-a-loadgenerator`
  **Ready**.
- Pods running in the spoke's `team-a` namespace: the web app, redis, a CNPG
  primary, and the load generator.
- The `Workspace` composition's policy — `enforce-naming-convention-team-a` — is
  reconciled on the spoke, which is D16 working. The hub runs its own Kyverno
  for platform pods but has no tenant policy.
- The hub has **no `team-a` namespace and no `user-space-*` Kustomizations**:
  the workload tier stays on the spoke.
- The spoke's gateway has **one listener (HTTP)**; the hub-only `dex-https`
  listener is removed by the tier.
- `curl -H 'Host: team-a.spokea.<hub-domain>' http://127.0.0.1:8080/` returns
  **200**, so a tenant hostname resolves through the spoke's own gateway.

The last two are the pair that matters: user space is delivered on the spoke and
nowhere else, and it is reachable from the laptop through the spoke's ingress.

All of it is now asserted by `verify-phase1.sh` (18 checks, 18 passes): the four
XRs' `Ready` conditions, pod health, the Kyverno policy, hub purity, the HTTP
200, the single listener, and both Prometheus queries. The topology was then
re-`up`'d unchanged in **2m00s** and stayed green — the earlier re-run died at
Cilium (§17.5), so idempotency is measured, not assumed.

---

## 18. Phase 3: a secret that is safe in git

The last Phase 0 gap was the tenant secret. `webapp.yaml` read
`team-a-app-config` with `optional: true` because nothing created it — a
placeholder that made the sample run, not a design.

Phase 3 is SOPS with age. Measured end to end, in the same topology:

| Step | Evidence |
|---|---|
| `shoulders secrets init` | wrote an age identity outside the checkout, mode `0600`, and `.sops.yaml` with the public recipient |
| `shoulders up` | `flux-system/sops-age` exists on the **hub and the spoke**; every Kustomization carries `decryption.provider: sops` |
| `sops -e` | the committed manifest is ciphertext; `sops -d` round-trips |
| flux reconcile | `Secret/team-a/team-a-app-config` exists on the spoke with `APP_GREETING=hello-from-sops` |
| the workload | a restarted web pod has `APP_GREETING=hello-from-sops` in its environment |

The sample encrypted file was then removed from the repository, deliberately:
an encrypted manifest is tied to the key that encrypted it. Committing one
would leave every other checkout with a Kustomization that can never become
Ready, and `optional: true` would not help — that flag is about the pod's
environment, not about decryption. `docs/secrets.md` carries the flow, and the
committed `webapp.yaml` keeps working without a key.

### 18.1 Three things measurement settled

- **Where sops looks for the key is platform-specific.** The documented
  `~/.config/sops/age/keys.txt` is not it on macOS: sops uses
  `os.UserConfigDir()`, which is `~/Library/Application Support`. Two failed
  decrypts found it. The CLI now defaults to the same path, so `sops -e` and
  `sops -d` work with no environment variables — the alternative was
  documenting `SOPS_AGE_KEY_FILE` in every command, forever.
- **Ordering is part of the contract.** Flux cannot decrypt until the Secret
  exists, so the CLI applies `sops-age` *before* the Kustomizations. Reversing
  it produces a first reconcile that fails for reasons that look unrelated to
  the key.
- **Decryption is opt-in, not always-on.** A Kustomization with a `decryption`
  block and no Secret never becomes Ready, so rendering the block
  unconditionally would break every topology that has not run
  `secrets init`. The key's presence is the switch, and
  `TestFluxDecryptsOnlyWhenAnAgeKeyIsConfigured` pins both directions.

### 18.2 What the key does not protect

The identity is in `flux-system` in every cluster, so read access to that
Secret is equivalent to read access to every tenant secret there. There is no
per-spoke key and no rotation automation; both are stated limits in
`docs/secrets.md`, not omissions. They matter on a real installation and not on
a laptop, and per-spoke keys are the natural next step — the CLI already
computes per-cluster material for Dex.

---

## 19. R1b, re-measured: the label was wrong and the scope was wide

The first mesh experiment concluded that
`k8s:io.cilium.network-name` selects a remote cluster (§8.1). It does not.
Cilium endpoints have no such label. The labels that exist are the usual
Kubernetes ones plus `io.cilium.k8s.policy.cluster`, and only the latter names
the cluster.

The old test policy had two rules:

```yaml
egress:
  - toEndpoints: [{ matchLabels: { k8s:io.kubernetes.pod.namespace: meshtest } }]
  - toEndpoints: [{ matchLabels: { k8s:io.cilium.network-name: meshb } }]
```

The remote pod was in `meshtest`, so the **first** rule admitted it. The second
rule matched nothing. The setting at the time was
`clustermesh.policyDefaultLocalCluster: false`, which made the namespace rule
span the whole mesh. That also explains the "policy selecting a non-existent
cluster still permitted traffic" observation: the policy's first rule was doing
the permitting.

### 19.1 The baseline, with the old setting

Three policies were applied in sequence to a server on `spokea` and clients on
both clusters. The remote client is the real test: it is in the hub's `r1b`
namespace and must be blocked unless a rule names the hub.

```
=== namespace only, policyDefaultLocalCluster=false ===
  spokea -> spokea: ALLOW
  hub    -> spokea: ALLOW          <-- the widening
=== cluster pinned to spokea ===
  spokea -> spokea: ALLOW
  hub    -> spokea: BLOCK
=== cluster pinned to hub ===
  spokea -> spokea: BLOCK
  hub    -> spokea: ALLOW
```

Both runtime ConfigMaps read `policy-default-local-cluster=false`:

```
vcluster-docker_hub    policy-default-local-cluster=false
vcluster-docker_spokea policy-default-local-cluster=false
```

`cilium clustermesh inspect-policy-default-local-cluster` enumerated what the
setting affects on the spoke:

```
CiliumNetworkPolicy 1/4
	✅ r1b/r1b-posture
	⚠️ team-a/default-deny
	⚠️ team-a/team-a-db-bucket-provisioner-egress
	⚠️ team-a/team-a-instance-gateway-ingress
NetworkPolicy 0/3
	⚠️ flux-system/allow-scraping
	⚠️ flux-system/allow-webhooks
	⚠️ flux-system/allow-egress
```

The one unaffected policy is the one that names a cluster. Every tenant and
Flux policy is namespace-only, and every dependency those policies protect —
CNPG, Redis, Garage — is installed in the same cluster. Nothing in the shipped
tree needs the wide scope.

### 19.2 The fix and the after-measurement

`meshValues` now sets `clustermesh.policyDefaultLocalCluster: true`, and
`TestMeshValuesScopesPolicyToTheLocalCluster` fails if it regresses. The same
three policies were re-run:

```
=== namespace only, policyDefaultLocalCluster=true ===
  spokea -> spokea: ALLOW
  hub    -> spokea: BLOCK          <-- the fix
=== cluster pinned to spokea ===
  spokea -> spokea: ALLOW
  hub    -> spokea: BLOCK
=== cluster pinned to hub ===
  spokea -> spokea: BLOCK
  hub    -> spokea: ALLOW
```

Both ConfigMaps now read `true`, pod WireGuard is on, and node encryption is
off; the last setting is what a cold install needs for the mesh datapath (§20):

```
vcluster-docker_hub    policy-default-local-cluster=true wireguard=true node=off
vcluster-docker_spokea policy-default-local-cluster=true wireguard=true node=off
```

The chart omits `encrypt-node` when node encryption is off, so a fresh install
has no key at all; a changed-values run writes it explicitly as `false`. In both
cases the agent reports `NodeEncryption: Disabled`.

So the safe default does what it says: a namespace-only policy is local, and a
rule that wants another cluster has to name it. The explicit pin still works in
both directions. The 18-check tenant verification passed after the change, so
the local policies did not lose anything they needed. The table above was
re-measured on the final cold pair with node encryption disabled; before that,
the one-directional mesh turned the `cluster=hub` row into a BLOCK.

The old conclusion — that `workspace-composition` needs a hub egress rule — is
withdrawn. Its dependencies are local, and D21 records the default.

---

## 20. WireGuard: pod traffic on, node encryption off

`platform.mesh.encryption: wireguard` is rendered into the Cilium chart as
`encryption.enabled: true`, `encryption.type: wireguard` and
`encryption.nodeEncryption: false`. Pod-to-pod traffic is encrypted. Node
encryption is deliberately off, and the reason is the most useful thing this
work measured.

With the final values, both clusters report:

```
Encryption: Wireguard [NodeEncryption: Disabled, cilium_wg0
  (Pubkey: dHAmbeHl/GVdDha1oDZ5RBpe0JpaAiLkATqG3B2IeDg=, Port: 51871, Peers: 3)]
```

`Peers: 3` is correct for two-node clusters: the other local node plus the two
remote nodes. Every agent has the same state; there is no opt-out split.

**Why node encryption is off.** The first implementation set
`nodeEncryption: true`. On the warm pair it looked healthy: Cilium's default
opt-out put the control-plane node in `OptedOut` and the worker in `Enabled`,
and pod traffic crossed the mesh. A **cold** install made the same config fail,
because it changed which node ran the clustermesh-apiserver:

```
hub     clustermesh-apiserver  -> hub      (control-plane, OptedOut)
spokea  clustermesh-apiserver  -> worker-1 (Enabled)
```

The hub's KVStoreMesh could not open TCP to the spoke's NodePort
(`spokea.mesh.cilium.io:32379`); `cilium clustermesh status` showed
`2/2 configured, 0/2 connected`, the troubleshoot command said
`Cannot establish TCP connection`, the hub side of the mesh stayed `0/1`, and
the `cluster=hub` posture rule (hub client → spokea server) went from ALLOWED to
BLOCKED. The same boundary broke the spoke's telemetry: the Alloy remote write
to `prometheus.hub.127.0.0.1.sslip.io` timed out, because that name resolves to
the hub node and the request is host-bound.

Disabling node encryption on both clusters and rolling the agents fixed all of
it at once:

- `cilium clustermesh status`: `2/2 connected`, KVStoreMesh `1/1`;
- the `cluster=hub` R1b rule: hub → spokea **ALLOWED** again;
- the hub Prometheus: `cluster="spokea"` series (41 at steady state on the
  final pair);
- the full tenant verification: 18/18.

The mesh control plane is already mTLS (Cilium's clustermesh certificates), and
pod traffic is WireGuard, so the only thing given up is encryption of host/node
traffic. That is the same trade Cilium makes by default on control-plane nodes;
this topology applies it on every node because the **inconsistency**, not the
encryption, was the failure.

**The rollout defect, and the second one behind it.** The first `up` after
enabling encryption left `cilium-dbg status` saying `Encryption: Disabled` even
though the HelmRelease had upgraded. The CLI publishes the runtime values and
then, when Flux owns the release, returns without running Helm — deliberately,
to avoid field-ownership fights (R8). A Helm upgrade that only changes the
values ConfigMap does not change the DaemonSet pod template, so the agents were
never restarted. Manual `kubectl rollout restart ds/cilium` produced WireGuard
immediately.

The first fix was `CiliumRuntimeValuesChanged` + `RestartCiliumWorkloads`.
That was necessary but not sufficient, and the cold pair found the gap:

1. The restart ran immediately after the CLI wrote the values ConfigMap, but
   **before** Flux had applied the HelmRelease. The agents re-read the old
   `cilium-config`.
2. Turning node encryption off makes the chart omit `encrypt-node`, and Helm's
   three-way merge does not delete a key that disappeared from the manifest.
   The live ConfigMap kept `encrypt-node: "true"`; the mesh stayed 0/1 until the
   key was written explicitly.

The final path in `EnsureCilium` is:

1. detect that the runtime values changed (the existing comparison);
2. read the current Helm revision **before** writing the values, so a fast Flux
   reconcile cannot make the wait race itself;
3. force the Cilium HelmRelease with `reconcile.fluxcd.io/requestedAt`;
4. wait for a newer **deployed** Helm revision;
5. wait for the policy key in `cilium-config`;
6. write `enable-wireguard` and `encrypt-node` explicitly, which also repairs
   keys Helm leaves behind;
7. roll the Cilium DaemonSet and operator.

Unchanged values are still a no-op: the final pair's second `up` left both
Cilium DaemonSets at their original creation timestamps and did not bump the
Helm revision (§22).

This was tested by toggling encryption on the live pair, not only by unit
tests:

| Run | `enable-wireguard` | `encrypt-node` | Agent status | Mesh |
|---|---|---|---|---|
| disable | `false` | `false` | `Encryption: Disabled` | 1/1 both sides |
| re-enable | `true` | `false` | `Encryption: Wireguard`, `Peers: 3` | 1/1 both sides |

Both `up` runs exited 0 (3:32 and 4:15), the Helm release moved from revision 4
to 5, and the R1b posture table was re-measured unchanged after the re-enable.

A second cold install with the final binary was the regression test: the hub and
spoke came up with `ClusterMesh 1/1`, `NodeEncryption: Disabled`, `Peers: 3`,
mesh `2/2 connected`, and `cluster="spokea"` telemetry, with no manual restart
or ConfigMap patch. That run is recorded in §22.

**Limits.** Node encryption is off; host/node traffic relies on the mesh's mTLS.
Cilium marks `nodeEncryption` deprecated and has said it will be removed. A
future opt-in would need the opt-out labels applied consistently on every node
(all or none), not just on control planes, and a test of the upstream
host-traffic behavior. The current choice is the one that survived a cold
install and a full tenant run.

---

## 21. The third spoke: what fits on a laptop

The code supports three clusters, and a third one was built: `spokeb` on ports
8081/8444, its own pod CIDR, and a Cilium install that joined the mesh. The hub
reported `2/2 remote clusters ready`, Dex had a `spokeb` client, and the spoke's
CNPG and Garage HelmReleases installed. Then its Flux platform reconcile began
in earnest, and the host ran out of control-plane CPU.

Measured during the failure, on a Docker VM with 11 CPUs and 16.7 GiB:

```
vcluster.cp.hub    260-319% CPU   2.1-3.6 GiB
vcluster.cp.spokea 137-329% CPU   2.0-2.6 GiB
vcluster.cp.spokeb 288-360% CPU   1.5-1.7 GiB
```

All three APIs became unreachable at once:

```
Get .../kustomizations: read tcp [::1]:64266->[::1]:12063:
  read: connection reset by peer
```

The hub and spokea both went `NOT-READY`, and Kyverno began rejecting writes on
spokea:

```
failed calling webhook "validate.kyverno.svc-fail":
  Post "https://kyverno-svc.kyverno.svc:443/validate/fail?timeout=10s":
  dial tcp 10.97.148.109:443: connect: no route to host
```

That webhook is fail-closed, which is the right default for admission control
and the wrong property during a resource collapse: the API stalls, the Kyverno
controller crashloops, and then every write that matches the webhook fails,
including the writes needed to recover. Deleting the crashlooping Kyverno pods
once the API returned (resetting the backoff) and waiting for readiness was the
recovery. The third cluster was then deleted, and both remaining APIs were ready
within about twenty seconds; control-plane CPU fell to 76% (spokea) and 118%
(hub).

Two contributing details are worth recording, because they cost time:

- **Each vind cluster's registry is cluster-local.** A plain `up` on a new
  cluster cannot pull an artifact pushed into another cluster's registry. The
  first `spokeb` run failed with
  `flux source OCIRepository/flux-system cannot be fetched`. `up --local` must
  be run per cluster (or a shared registry used). This is a dev-loop property,
  not a production one.
- **A stale source tag becomes a retry storm.** Pushing a new artifact is not
  enough if the `OCIRepository` still points at the old tag; forcing its
  reconcile with the `reconcile.fluxcd.io/requestedAt` annotation is what makes
  the new revision visible. On `spokeb` the combination of a failed source and
  a platform install produced a retry loop that added load to an already
  saturated control plane.

The conclusion is not that the topology is wrong. It is that **hub + one spoke
is this laptop's ceiling at the `small` profile**, which is what D10 says. A
third spoke needs more CPU (or a deliberately lighter spoke profile), and the
real answer is the deferred Talos/Hetzner target, where the control planes are
not three containers sharing one Docker VM. The third-spoke experiment is
therefore a measured scale limit, not an unfinished feature: the configuration,
ports, mesh, Dex registration and artifact path all work, and the point of
failure was host capacity.

---

## 22. The final lifecycle, measured

The topology's acceptance test is not a one-way install: `down` and `up` have to
work from cold, and a second `up` has to be a no-op for the CNI.

| Run | Result |
|---|---|
| `down` (both clusters) | 18s warm; 24.7s on the first cold attempt |
| cold `up --local`, first binary | 12m18s (`real 737.86`), exit 0; the mesh needed the node-encryption repair (§20) |
| cold `up --local`, final binary | **12m28s** (`real 747.76`), exit 0 |
| idempotent `up --local`, final pair | 3m05s (`real 184.79`), exit 0; Cilium pods and Helm revision unchanged |

The final cold run is the one that matters for the code in this tree:

- hub and spokea both came up with `Encryption: Wireguard
  [NodeEncryption: Disabled, Peers: 3]`;
- `cilium clustermesh status` reported `2/2 connected` and KVStoreMesh `1/1`;
- the hub Prometheus had `cluster="spokea"` series (37 within two minutes, 41 at
  steady state);
- the R1b table was exact: namespace-only local ALLOW / remote BLOCK;
  `cluster=spokea` local ALLOW / hub BLOCK; `cluster=hub` local BLOCK / hub
  ALLOW; both ConfigMaps reported `policy-default-local-cluster=true`;
- `verify-phase1.sh` passed **18/18**, including the tenant, the spoke's own
  ingress, hub purity, Kyverno on both tiers, and the spoke→hub telemetry.

No manual Cilium restart, ConfigMap patch, or mesh repair was needed on the
final cold run. That is the difference from the first cold run: the code now
sets `nodeEncryption: false` before the agents start, so the control-plane
opt-out split never exists.

The idempotent run is the other half of the lifecycle. It re-pushed the artifact
and reconciled the platform, but the Cilium values were unchanged, so
`EnsureCilium` took the early return: both DaemonSets kept their original
creation timestamps (hub `23:21:10Z`, spokea `23:27:21Z`), the Helm release
stayed at revision 2, and `verify-phase1.sh` still passed 18/18. A routine `up`
does not bounce the CNI.
