# Identity and access

Shoulders ships **Dex** as OIDC provider. Grafana and Headlamp authenticate via Dex.

Default sample users:

- `admin@example.com` / `password`
- `developer@example.com` / `password`
- `viewer@example.com` / `password`

```bash
shoulders dashboard
shoulders portal
```

`dashboard` tries the configured Grafana gateway host first (OIDC via Dex), default
`http://grafana.localhost`, falling back to `http://localhost:3000` port-forward with
printed admin credentials. Retrieve the admin password manually:

```bash
kubectl get secret -n observability kube-prometheus-stack-grafana -o jsonpath='{.data.admin-password}' | base64 -d
```
