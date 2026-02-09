# Shoulders UI

Lightweight developer portal for the Shoulders IDP. It serves a static web UI and provides a local API layer that
connects to Kubernetes using your kubeconfig.

## Features

- Service catalog with live counts and resource previews
- Deploy wizard with YAML preview and apply
- Grafana embed panel
- Context switcher + namespace filters
- Team management placeholder

## Requirements

- Node.js 18+ (Node 20 recommended)
- Access to a Kubernetes cluster via kubeconfig

## Run locally

```bash
npm install
npm run dev
```

Then open `http://localhost:8787`.

If you want to use a non-default kubeconfig:

```bash
KUBECONFIG=/path/to/kubeconfig npm run dev
```

## Scripts

- `npm run dev` - start the local portal server
- `npm test` - run the node test suite

## Environment variables

- `PORT` - override the default port (8787)
- `KUBECONFIG` - path to a kubeconfig file
- `SHOULDERS_MOCK=1` - serve mocked API data (used for tests)

## API endpoints

- `GET /api/summary` - counts and resource lists
- `GET /api/contexts` - kubeconfig contexts
- `POST /api/context` - switch current context
- `GET /api/namespaces` - namespace list
- `POST /api/apply` - apply YAML documents

## Notes

The `/api/apply` endpoint uses the Kubernetes API directly. It will create resources if they do not exist and replace
existing resources using the latest `resourceVersion`.
