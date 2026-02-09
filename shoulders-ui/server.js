import path from "path";
import { fileURLToPath } from "url";
import express from "express";
import yaml from "yaml";
import {
  KubeConfig,
  CustomObjectsApi,
  CoreV1Api,
  KubernetesObjectApi,
} from "@kubernetes/client-node";

const port = process.env.PORT || 8787;
const __filename = fileURLToPath(import.meta.url);
const rootDir = path.dirname(__filename);

const group = "shoulders.io";
const version = "v1alpha1";
let kubeConfig;
let kubeConfigError;
let activeContext;
const mockMode = () => process.env.SHOULDERS_MOCK === "1";

function getKubeConfig() {
  if (!kubeConfig && !kubeConfigError) {
    const candidate = new KubeConfig();
    try {
      candidate.loadFromDefault();
      if (activeContext) {
        candidate.setCurrentContext(activeContext);
      }
      kubeConfig = candidate;
    } catch (error) {
      kubeConfigError = extractError(error);
    }
  }
  if (kubeConfigError) {
    throw new Error(kubeConfigError);
  }
  return kubeConfig;
}

function resetKubeConfig(contextName) {
  const candidate = new KubeConfig();
  candidate.loadFromDefault();
  if (contextName) {
    candidate.setCurrentContext(contextName);
  }
  kubeConfig = candidate;
  kubeConfigError = null;
}

function getClients() {
  const kc = getKubeConfig();
  return {
    custom: kc.makeApiClient(CustomObjectsApi),
    core: kc.makeApiClient(CoreV1Api),
    objects: KubernetesObjectApi.makeApiClient(kc),
  };
}

function extractError(error) {
  if (!error) return "Unknown error";
  if (error.body && error.body.message) return error.body.message;
  if (error.message) return error.message;
  return String(error);
}

function isAlreadyExists(error) {
  const code = error?.statusCode || error?.body?.code;
  if (code === 409) return true;
  if (error?.body?.reason === "AlreadyExists") return true;
  const message = extractError(error);
  return message.toLowerCase().includes("already exists");
}

function unwrapResponse(response) {
  if (!response) return null;
  return response.body ?? response;
}

async function listCustomObjects(plural) {
  const { custom } = getClients();
  try {
    const response = await custom.listClusterCustomObject({ group, version, plural });
    const body = unwrapResponse(response);
    const items = body?.items || [];
    return { items, error: null };
  } catch (error) {
    return { items: [], error: extractError(error) };
  }
}

function mapItems(items) {
  return items.map((item) => {
    const metadata = item.metadata || {};
    return {
      name: metadata.name,
      namespace: metadata.namespace || "",
      createdAt: metadata.creationTimestamp || "",
    };
  });
}

function createApp() {
  const app = express();
  app.use(express.static(rootDir));
  app.use(express.json({ limit: "1mb" }));

  app.get("/api/contexts", (_req, res) => {
    if (mockMode()) {
      res.json({
        current: "kind-shoulders",
        contexts: ["kind-shoulders", "prod-cluster"],
      });
      return;
    }
    try {
      const kc = getKubeConfig();
      const contexts = kc.getContexts().map((ctx) => ctx.name);
      res.json({
        current: kc.getCurrentContext(),
        contexts,
      });
    } catch (error) {
      res.status(500).json({ error: extractError(error), contexts: [] });
    }
  });

  app.post("/api/context", (req, res) => {
    const context = req.body ? req.body.context : null;
    if (!context) {
      res.status(400).json({ error: "Missing context name." });
      return;
    }
    if (mockMode()) {
      res.json({ current: context });
      return;
    }
    try {
      resetKubeConfig(context);
      activeContext = context;
      res.json({ current: context });
    } catch (error) {
      res.status(500).json({ error: extractError(error) });
    }
  });

  app.get("/api/namespaces", async (_req, res) => {
    if (mockMode()) {
      res.json({ items: [{ name: "team-a" }, { name: "team-b" }] });
      return;
    }
    try {
      const { core } = getClients();
      const namespaces = await core.listNamespace();
      const body = unwrapResponse(namespaces);
      const items = (body?.items || []).map((item) => ({
        name: item.metadata?.name || "",
      }));
      res.json({ items });
    } catch (error) {
      res.status(500).json({ error: extractError(error), items: [] });
    }
  });

  app.get("/api/summary", async (_req, res) => {
    const warnings = [];
    if (mockMode()) {
      res.json({
        context: "kind-shoulders",
        cluster: "kind-shoulders",
        server: "https://127.0.0.1:6443",
        counts: {
          workspaces: 2,
          webApplications: 3,
          stateStores: 1,
          eventStreams: 1,
        },
        resources: {
          workspaces: [
            { name: "team-a", namespace: "", createdAt: "" },
            { name: "team-b", namespace: "", createdAt: "" },
          ],
          webApplications: [
            { name: "web-a", namespace: "team-a", createdAt: "" },
            { name: "web-b", namespace: "team-a", createdAt: "" },
            { name: "web-c", namespace: "team-b", createdAt: "" },
          ],
          stateStores: [{ name: "state-a", namespace: "team-a", createdAt: "" }],
          eventStreams: [{ name: "events-a", namespace: "team-a", createdAt: "" }],
        },
        warnings,
      });
      return;
    }

    let currentContext = null;
    let currentCluster = null;
    try {
      const kc = getKubeConfig();
      currentContext = kc.getCurrentContext();
      currentCluster = kc.getCurrentCluster();
    } catch (error) {
      warnings.push(`kubeconfig: ${extractError(error)}`);
      res.json({
        context: null,
        cluster: null,
        server: null,
        counts: {
          workspaces: 0,
          webApplications: 0,
          stateStores: 0,
          eventStreams: 0,
        },
        resources: {
          workspaces: [],
          webApplications: [],
          stateStores: [],
          eventStreams: [],
        },
        warnings,
      });
      return;
    }

    const [workspaces, webApplications, stateStores, eventStreams] = await Promise.all([
      listCustomObjects("workspaces"),
      listCustomObjects("webapplications"),
      listCustomObjects("statestores"),
      listCustomObjects("eventstreams"),
    ]);

    let workspaceItems = workspaces.items;
    if (workspaces.error) {
      warnings.push(`workspaces: ${workspaces.error}`);
      try {
        const { core } = getClients();
        const namespaces = await core.listNamespace();
        const body = unwrapResponse(namespaces);
        workspaceItems = body?.items || [];
      } catch (error) {
        warnings.push(`namespaces: ${extractError(error)}`);
      }
    }

    const workspaceRefs = mapItems(workspaceItems);
    const webAppRefs = mapItems(webApplications.items);
    const stateStoreRefs = mapItems(stateStores.items);
    const eventStreamRefs = mapItems(eventStreams.items);

    res.json({
      context: currentContext || null,
      cluster: currentCluster ? currentCluster.name : null,
      server: currentCluster ? currentCluster.server : null,
      counts: {
        workspaces: workspaceRefs.length,
        webApplications: webAppRefs.length,
        stateStores: stateStoreRefs.length,
        eventStreams: eventStreamRefs.length,
      },
      resources: {
        workspaces: workspaceRefs,
        webApplications: webAppRefs,
        stateStores: stateStoreRefs,
        eventStreams: eventStreamRefs,
      },
      warnings,
    });
  });

  app.get("/api/resources/:kind", async (req, res) => {
    if (mockMode()) {
      res.json({ items: [] });
      return;
    }
    try {
      getKubeConfig();
    } catch (error) {
      res.status(500).json({ error: `kubeconfig: ${extractError(error)}` });
      return;
    }
    const kind = (req.params.kind || "").toLowerCase();
    const map = {
      workspace: "workspaces",
      workspaces: "workspaces",
      webapplication: "webapplications",
      webapplications: "webapplications",
      webapp: "webapplications",
      webapps: "webapplications",
      statestore: "statestores",
      statestores: "statestores",
      eventstream: "eventstreams",
      eventstreams: "eventstreams",
    };

    const plural = map[kind];
    if (!plural) {
      res.status(400).json({ error: `Unknown kind: ${kind}` });
      return;
    }

    const result = await listCustomObjects(plural);
    if (result.error) {
      res.status(500).json({ error: result.error, items: [] });
      return;
    }

    res.json({ items: mapItems(result.items) });
  });

  app.post("/api/apply", async (req, res) => {
    const yamlText = req.body ? req.body.yaml : null;
    if (!yamlText) {
      res.status(400).json({ errors: ["Missing yaml payload."] });
      return;
    }

    let objects;
    try {
      objects = yaml.parseAllDocuments(yamlText)
        .map((doc) => doc.toJSON())
        .filter(Boolean);
    } catch (error) {
      res.status(400).json({ errors: [`YAML parse failed: ${extractError(error)}`] });
      return;
    }

    const applied = [];
    const errors = [];
    const clusterScopedKinds = new Set(["Workspace", "Namespace"]);

    if (mockMode()) {
      for (const object of objects) {
        if (!object || !object.kind || !object.apiVersion || !object.metadata?.name) {
          errors.push("Manifest is missing apiVersion, kind, or metadata.name.");
          continue;
        }
        object.metadata = object.metadata || {};
        if (!object.metadata.namespace && !clusterScopedKinds.has(object.kind)) {
          object.metadata.namespace = req.body.namespace || "default";
        }
        applied.push({
          kind: object.kind,
          name: object.metadata.name,
          namespace: object.metadata.namespace || "",
        });
      }
      res.json({ applied, errors });
      return;
    }

    try {
      getKubeConfig();
    } catch (error) {
      res.status(500).json({ errors: [`kubeconfig: ${extractError(error)}`] });
      return;
    }

    const client = getClients().objects;

    for (const object of objects) {
      if (!object || !object.kind || !object.apiVersion || !object.metadata?.name) {
        errors.push("Manifest is missing apiVersion, kind, or metadata.name.");
        continue;
      }

      object.metadata = object.metadata || {};
      if (!object.metadata.namespace && !clusterScopedKinds.has(object.kind)) {
        object.metadata.namespace = req.body.namespace || "default";
      }

      try {
        await client.create(object);
        applied.push({
          kind: object.kind,
          name: object.metadata.name,
          namespace: object.metadata.namespace || "",
        });
        continue;
      } catch (error) {
        if (!isAlreadyExists(error)) {
          errors.push(`${object.kind}/${object.metadata.name}: ${extractError(error)}`);
          continue;
        }
      }

      try {
        const existing = await client.read(object);
        const body = unwrapResponse(existing);
        if (body?.metadata?.resourceVersion) {
          object.metadata.resourceVersion = body.metadata.resourceVersion;
        }
        await client.replace(object);
        applied.push({
          kind: object.kind,
          name: object.metadata.name,
          namespace: object.metadata.namespace || "",
        });
      } catch (error) {
        errors.push(`${object.kind}/${object.metadata.name}: ${extractError(error)}`);
      }
    }

    res.json({ applied, errors });
  });

  return app;
}

const isMain = process.argv[1] === fileURLToPath(import.meta.url);
if (isMain) {
  const app = createApp();
  app.listen(port, () => {
    console.log(`Shoulders UI listening on http://localhost:${port}`);
  });
}

export { createApp };
