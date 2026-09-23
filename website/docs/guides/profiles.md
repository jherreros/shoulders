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

Profile overlays live under `2-addons/profiles/` and are valid Flux/Kustomize paths:

```bash
kubectl apply -k 2-addons/profiles/<profile>/flux
SHOULDERS_PROFILE=small 2-addons/install-addons.sh
shoulders --set platform.profile=small up
```
