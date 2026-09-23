# Workloads

Workloads run non-HTTP processes as worker Deployments, one-shot Jobs or CronJobs.
They are **namespace-scoped**.

```yaml
apiVersion: shoulders.io/v1alpha1
kind: Workload
metadata:
  name: team-a-loadgenerator
  namespace: team-a
spec:
  type: cronjob
  image: curlimages/curl
  tag: latest
  schedule: "*/5 * * * *"
  args:
    - -fsS
    - http://team-a-instance
```

Fields: `type` (`worker`|`job`|`cronjob`, default `worker`), `image`/`tag`,
`replicas`, `schedule`, `command`/`args`, `env`, `envFrom`, `volumes`,
`volumeMounts`, `resources`, `securityContext`.

CLI:

```bash
shoulders workload worker <name>
shoulders workload job <name>
shoulders workload cron <name>   # --schedule required
shoulders workload list
```
