# API overview

Platform abstractions are Crossplane Composite Resources. Reference pages under this
section are **auto-generated** in CI from the XRDs in
`2-addons/manifests/crossplane/definitions/` — do not edit generated files by hand.

- `Workspace` — cluster-scoped tenant (`workspace-xrd.yaml`)
- `WebApplication` — namespace-scoped HTTP service (`application-xrd.yaml`)
- `Workload` — worker/job/cronjob (`workload-xrd.yaml`)
- `StateStore` — postgres/redis/object storage (`state-store-xrd.yaml`)
- `EventStream` — Kafka + topics (`event-stream-xrd.yaml`)

Regenerate locally:

```bash
./scripts/generate-api-docs.sh
```

Conceptual field tables live under Concepts; generated pages carry the full schema.
