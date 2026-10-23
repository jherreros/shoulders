# User space

This is where tenants' manifests live, and it is delivered by flux like the
platform is — not applied by hand.

```
3-user-space/
  workspaces/     Workspace XRs. One per tenant.
  team-a/         The sample tenant's XRs.
```

Flux reconciles them with two Kustomizations, in this order:

| Kustomization | Path | Why it is separate |
| --- | --- | --- |
| `user-space-workspaces` | `./3-user-space/workspaces` | A `Workspace` owns its namespace, so the namespace does not exist until the `Workspace` is ready. |
| `user-space-apps` | `./3-user-space/team-a` | The tenant's XRs are namespaced, so they depend on the one above. |

Both are **workload tier**: a hub deletes them. A monocluster is a hub, so it
does not reconcile this directory either — which is what it did before the
split.

## The sample

`team-a` is a reference tenant. It exercises four compositions:

- `Workspace/team-a` — the namespace, its default-deny network policy and its
  Kyverno naming policy.
- `WebApplication/team-a-instance` — a Deployment, a Service and an `HTTPRoute`
  on the cluster's own gateway.
- `StateStore/team-a-db` — CloudNativePG, Redis and a Garage bucket.
- `Workload/team-a-loadgenerator` — a CronJob that curls the webapp.

Two things about it are deliberate:

- **The image is `nginx-unprivileged`.** The sample sets `runAsNonRoot: true`,
  which the stock `nginx` image does not satisfy — it starts as root and the
  kubelet refuses it. The unprivileged image runs as uid 101 and listens on
  8080.
- **The load generator names its uid.** `curlimages/curl` declares
  `USER curl_user`, and `runAsNonRoot` cannot verify a non-numeric user, so the
  kubelet refuses it with `cannot verify user is non-root`. `runAsUser: 100`
  makes the check decidable.

## Secrets

`webapp.yaml` reads `team-a-app-config` with `optional: true`, so the sample
starts on a machine that has no age key. To use a real secret, run
`shoulders secrets init`, encrypt a Secret to the printed recipient as
`app-config.sops.yaml`, and list it in `kustomization.yaml`. See
[`docs/secrets.md`](../docs/secrets.md) for the full flow and its limits.
- **The host is `team-a.${SHOULDERS_WORKLOAD_DOMAIN}`.** The platform sets that
  variable per cluster: on a spoke it is `<spoke>.<hub domain>`, so two spokes
  cannot claim the same hostname and the hub still owns the only DNS zone.

`event-stream.yaml` is a Kafka example and is not listed in the kustomization:
the `small` profile removes the `EventStream` XRD, so it would not apply there.
Profiles that keep the Kafka composition can add it back.
