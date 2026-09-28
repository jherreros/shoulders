# Profiles

`platform.profile` selects the addon footprint. `medium` is the default.

| Profile | Intended target | Local vind topology | Event Streams | Optional security/reporting |
|---|---|---|---|---|
| `small` | Laptops and small clusters | control plane + 1 worker | No | Kyverno admission only |
| `medium` | Default local platform | control plane + 2 workers | Yes | Trivy, Falco, Policy Reporter |
| `large` | Full local platform | control plane + 3 workers | Yes | Trivy, Falco, Policy Reporter |

`small` keeps the core IDP, basic Grafana/Prometheus, Dex, Headlamp, Crossplane,
Kyverno admission, CNPG and Garage, but omits Loki/Tempo/Alloy, Hubble UI, Trivy,
Falco and Policy Reporter.

## Minimum hardware

Contributor version of this table (kept in sync — update both together) lives
in `CONTRIBUTING.md` under "Cluster sizes and resource requirements".

vind clusters share the Docker Desktop VM, so profile weight shows up as node
`DiskPressure`, evictions and webhook timeouts — not just slowness. Measured
reference (Apple Silicon laptop, Docker Desktop 11 CPUs):

- `small`: fully healthy — 37/37 pods, Flux/Crossplane/Gateway green. A full
  Bank of Anthos (9 workloads + 2×CNPG) fits comfortably on the single worker.
- `medium`: never converged on an 11 CPU / ~12 GiB VM — `DiskPressure` on all
  nodes, mass evictions, cascading Kyverno webhook timeouts. The platform logic
  was fine; the hardware budget was not. Update: `medium` **did** converge in
  ~14 min (64/64 pods, no pressure) on 11 CPU / 16 GiB / 100 GiB — the extra
  memory appears to tip it. (That run also caught the default profile shipping
  a nonexistent k8s `v1.37.0` vind image; pinned back to `v1.36.0`.)

Guidance:

| Profile | Docker CPUs | Docker memory | Free host disk | Where |
|---|---|---|---|---|
| `small` | ≥ 8 | 8–12 GiB | ~25 GiB (VM disk + images + spare bundle) | Laptops |
| `medium` | ≥ 8 | ≥ 16 GiB | ~40 GiB | Devserver or CI runners |
| `large` | ≥ 8 | 32 GiB | 60+ GiB | Devservers only (week-long Prometheus retention) |

`shoulders up` pre-flights host ports `80`/`443` and Docker disk headroom, but
treat a red `medium` `status` on a laptop as a resource problem first: check
`kubectl describe nodes` for pressure before suspecting a code regression.
Trivy/Falco can be scaled to zero on constrained machines (see
[Break-glass](../operations/break-glass.md)).

Profile overlays live under `2-addons/profiles/` and are valid Flux/Kustomize paths:

```bash
kubectl apply -k 2-addons/profiles/<profile>/flux
SHOULDERS_PROFILE=small 2-addons/install-addons.sh
shoulders --set platform.profile=small up
```
