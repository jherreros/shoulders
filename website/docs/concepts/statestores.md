# StateStores

StateStores provision database, cache and object storage. They are
**namespace-scoped**. PostgreSQL, Redis and object storage can be independently
enabled or disabled.

```yaml
apiVersion: shoulders.io/v1alpha1
kind: StateStore
metadata:
  name: team-a-db
  namespace: team-a
spec:
  postgresql:
    database: app
    secretName: team-a-db-app-secret
    databases:
      - team-a-01
  redis:
    enabled: true
    replicas: 1
  objectStorage:
    enabled: true
    buckets:
      - name: team-a-assets
        secretName: team-a-assets-s3
        read: true
        write: true
```

- **PostgreSQL**: CloudNativePG Cluster with app user, per-StateStore Secret
  (`<name>-app-secret` by default) and extra databases owned by the app user.
  Connect via `<state-store>-rw.<namespace>.svc.cluster.local` with Secret keys
  `username`/`password`. Dev-tier credential defaults are `app` / `password` —
  change them outside local development.
- **Redis**: Deployment + Service (`<name>-redis`).
- **Object storage**: Garage bucket per entry, access key with requested permissions,
  and a Secret containing `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
  `AWS_DEFAULT_REGION`, `AWS_ENDPOINT_URL`, `S3_BUCKET`.

Bootstrap SQL (repeatable, files via shell substitution):

```bash
shoulders infra add-db <name> \
  --init-sql "CREATE EXTENSION IF NOT EXISTS pgcrypto;" \
  --init-sql "$(cat schema.sql)"
```

Compute: PostgreSQL instances are BestEffort by default. Give them requests
(so they don't die first under node pressure) with `--tier prod` (implies
`250m`/`512Mi` requests) or explicit `--cpu-request`, `--memory-request`,
`--cpu-limit`, `--memory-limit`:

```bash
shoulders infra add-db <name> --tier prod
shoulders infra add-db <name> --cpu-request 250m --memory-request 512Mi
```

CLI:

```bash
shoulders infra add-db <name>       # --type postgres|redis, --tier dev|prod
shoulders infra add-bucket <name>   # --bucket, --secret
shoulders infra list
shoulders infra delete <name>
```
