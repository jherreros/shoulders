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
shoulders app init backend-api --image myapp:latest --host api.local --port 8080
shoulders infra add-db backend-db --type postgres --tier dev
shoulders status
```

## Application with Redis cache

```bash
shoulders workspace create cache-demo
shoulders workspace use cache-demo
shoulders app init cache-demo-app --image myapp:latest --host app.local
shoulders infra add-db cache-demo-redis --type redis
```

## Application with object storage

```bash
shoulders workspace create assets-demo
shoulders workspace use assets-demo
shoulders app init assets-demo-api --image myapp:latest --host assets.local
shoulders infra add-bucket assets-demo-bucket --bucket assets-demo-files
```

## Full-stack: app + database + Kafka

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
  --config retention.ms=604800000
```

## Dry-run to inspect generated resources

```bash
shoulders app init demo-test --image nginx --dry-run
```
