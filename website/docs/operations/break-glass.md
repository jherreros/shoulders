# Break-glass runbook

Last-resort procedures for a wedged platform, learned from a disk-pressure
collapse on a laptop `medium` cluster. These bypass safety rails — prefer
prevention (disk headroom, realistic [profile minimums](../guides/profiles.md))
and use this page only when the platform cannot heal itself.

## Disk-pressure death spiral

Symptoms: nodes report `DiskPressure`, mass pod evictions
(`ContainerStatusUnknown`/`Evicted`), Kyverno admission timeouts, Flux unable
to reconcile. Root cause chain: local images + pulls fill the Docker disk →
kubelet evicts → Kyverno dies → fail-closed webhooks freeze ALL pod mutations
→ Flux can't heal.

Recovery, in order:

```bash
docker builder prune
docker image prune
# Flip failing Kyverno admission webhooks to Ignore (repeat up to 3 rounds;
# Flux reverts to Fail by design — re-apply as needed until Kyverno is stable).
for kind in validatingwebhookconfiguration mutatingwebhookconfiguration; do
  for cfg in $(kubectl get "$kind" -o name | grep kyverno); do
    count=$(kubectl get "$cfg" -o jsonpath='{.webhooks[*].name}' | wc -w | tr -d ' ')
    for i in $(seq 0 $((count - 1))); do
      kubectl patch "$cfg" --type='json' \
        -p="[{\"op\": \"replace\", \"path\": \"/webhooks/$i/failurePolicy\", \"value\": \"Ignore\"}]"
    done
  done
done
kubectl delete pods --all-namespaces --field-selector=status.phase=Failed
shoulders stop
shoulders start
```

Also clear stale scheduling taints left behind:

```bash
kubectl taint nodes --all node.kubernetes.io/disk-pressure-
```

Do not prune local images until the platform is green again — you may need
them for `load-image` if the node image store was affected.

## Trivy scan storms on small hardware

`medium` runs Trivy Operator, whose scan jobs can OOM-loop on constrained
machines and churn the API server. Proven mitigation (twice): park it.

```bash
kubectl scale deploy trivy-operator -n trivy-system --replicas=0
```

Resume explicitly only on bigger hardware. Verify the resume held before
walking away — the collapse recurs within minutes if the machine is too small.

## Flux suspends and Kyverno admission

```bash
flux suspend helmrelease kyverno -n flux-system          # ambiguous state possible on timeouts; verify
flux resume helmrelease kyverno -n flux-system
kubectl scale deploy trivy-operator -n trivy-system --replicas=1  # only on bigger iron
```

Keep Kyverno webhooks on `Ignore` until admission holds steady (watch webhook
pod restarts); Flux reverts to `Fail` by itself, which is the desired end
state — but only once the cluster is stable.

## CloudNativePG last resort

A wedged CNPG Cluster (finalizers blocking deletion) plus manual PVC deletes
recovered postgres in the field. This destroys data — reprovision and reseed
afterwards, and prefer it only when the operator cannot make progress.

## Handover checklist pattern

After any break-glass session, write down what is still degraded: webhook
policies, suspended releases, scaled-to-zero operators, pruned images. The
Astro incident's checklist (postgres reseeded, webhook flips documented,
per-service OTel overrides, ConfigMap fixes live, Kyverno/Trivy/Flux resume
items open) is the template — tribal memory evaporates.
