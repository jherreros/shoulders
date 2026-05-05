# Deployment Patterns

## Simple web application

```bash
shoulders workspace create demo
shoulders workspace use demo
shoulders app init demo-nginx --image nginx:latest --host demo.local
shoulders app list
```

## Application with PostgreSQL database

```bash
shoulders workspace create backend
shoulders workspace use backend
shoulders app init backend-api --image myapp:latest --host api.local --port 8080 \
  --env LOG_LEVEL=info --readiness-path /ready --cpu-request 100m
shoulders infra add-db backend-db --type postgres --tier dev --database app --databases ledger,accounts
shoulders status
```

## Application with Redis cache

```bash
shoulders workspace create cache-demo
shoulders workspace use cache-demo
shoulders app init cache-demo-app --image myapp:latest --host app.local
shoulders infra add-db cache-demo-redis --type redis
```

## Internal backend service

```bash
shoulders workspace create backend-only
shoulders workspace use backend-only
shoulders app init backend-only-api --image api:latest --internal --port 8080 \
  --env-from-secret backend-only-config --readiness-path /ready
```

## Background worker and scheduled job

```bash
shoulders workspace create jobs-demo
shoulders workspace use jobs-demo
shoulders workload worker jobs-demo-worker --image worker:latest --replicas 2 --env QUEUE=default
shoulders workload cron jobs-demo-load --image curlimages/curl:latest --schedule "*/5 * * * *" \
  --arg -fsS --arg http://jobs-demo-api
```

## Local image development

```bash
shoulders workspace create local-dev
shoulders workspace use local-dev
shoulders app build-image local-dev-api:dev .
shoulders app init local-dev-api --image local-dev-api:dev --host local-dev.local --port 8080
```

## Application with object storage

```bash
shoulders workspace create assets-demo
shoulders workspace use assets-demo
shoulders app init assets-demo-api --image myapp:latest --host assets.local
shoulders infra add-bucket assets-demo-bucket --bucket assets-demo-files
```

## Full-stack: app + database + Kafka

Requires `platform.profile: medium` or `platform.profile: large` because the `small` profile omits Event Streams.

```bash
shoulders workspace create platform
shoulders workspace use platform
shoulders app init platform-api --image api:latest --host api.local --port 8080
shoulders infra add-db platform-db --type postgres --tier prod
shoulders infra add-db platform-cache --type redis
shoulders infra add-bucket platform-assets --bucket platform-assets
shoulders infra add-stream platform-events --topics "orders,notifications,audit" --partitions 5
shoulders infra list
shoulders logs platform-api
```

## Multi-service deployment

```bash
shoulders workspace create ecommerce
shoulders workspace use ecommerce

# Frontend
shoulders app init ecommerce-web --image frontend:latest --host shop.local

# Backend API
shoulders app init ecommerce-api --image api:latest --host api.shop.local --port 3000

# Database
shoulders infra add-db ecommerce-db --type postgres --tier prod

# Cache
shoulders infra add-db ecommerce-cache --type redis

# Event streaming
shoulders infra add-stream ecommerce-events --topics "orders,inventory,notifications" \
  --topic-config retention.ms=604800000
```

## Dry-run to inspect generated resources

```bash
shoulders app init demo-test --image nginx --dry-run
```
