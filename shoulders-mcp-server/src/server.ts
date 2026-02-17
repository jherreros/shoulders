#!/usr/bin/env node
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ErrorCode,
  ListResourcesRequestSchema,
  ListToolsRequestSchema,
  McpError,
  ReadResourceRequestSchema
} from "@modelcontextprotocol/sdk/types.js";
import { existsSync } from "node:fs";
import path from "node:path";

import { HttpError, PortForwardError, ValidationError } from "./errors.js";
import { logger } from "./logger.js";
import { buildLokiQueryParams, buildTempoTracePath } from "./observability.js";
import {
  buildKubeClients,
  getCurrentWorkspace,
  httpGetJson,
  isNotFoundError,
  listKindClusters,
  readFileText,
  resolveRepoRoot,
  resolveKubeconfigPath,
  setCurrentContext,
  setCurrentWorkspace,
  truncate,
  validateImage,
  validateK8sName,
  withPortForward
} from "./utils.js";

interface CreateWorkspaceArgs {
  name: string;
}

interface UseWorkspaceArgs {
  name: string;
}

interface DeleteWorkspaceArgs {
  name: string;
}

interface DeployAppArgs {
  name: string;
  namespace: string;
  image: string;
  tag?: string;
  replicas?: number;
  host?: string;
  port?: number;
}

interface AppStatusArgs {
  name: string;
  namespace: string;
}

interface AppDeleteArgs {
  name: string;
  namespace: string;
}

interface AppListArgs {
  namespace?: string;
}

interface AppLogsArgs {
  name: string;
  namespace?: string;
  limit?: number;
  sinceSeconds?: number;
}

interface TraceArgs {
  traceId: string;
}

interface AddDatabaseArgs {
  name: string;
  namespace: string;
  type?: "postgres" | "postgresql" | "redis";
  tier?: "dev" | "prod";
}

interface AddStreamArgs {
  name: string;
  namespace: string;
  topics: string[];
  partitions?: number;
  replicas?: number;
  config?: Record<string, string>;
}

interface InfraListArgs {
  namespace?: string;
}

interface InfraDeleteArgs {
  name: string;
  namespace: string;
}

interface UseClusterArgs {
  name: string;
}

const repoRoot = resolveRepoRoot();

const observabilityNamespace = process.env.SHOULDERS_OBSERVABILITY_NAMESPACE || "observability";
const lokiService = process.env.SHOULDERS_LOKI_SERVICE || "loki";
const tempoService = process.env.SHOULDERS_TEMPO_SERVICE || "tempo";
const lokiRemotePort = Number(process.env.SHOULDERS_LOKI_REMOTE_PORT || 3100);
const tempoRemotePort = Number(process.env.SHOULDERS_TEMPO_REMOTE_PORT || 3100);
const portForwardTimeoutMs = Number(process.env.SHOULDERS_MCP_PORT_FORWARD_TIMEOUT_MS || 15000);

const shouldersGroup = "shoulders.io";
const shouldersVersion = "v1alpha1";

const resourceIndex: Record<string, { path: string; name: string; description: string }> = {
  "shoulders://schemas/workspace": {
    path: path.join(repoRoot, "2-addons", "manifests", "crossplane", "definitions", "workspace-xrd.yaml"),
    name: "Workspace Schema",
    description: "Crossplane XRD for Workspaces"
  },
  "shoulders://schemas/webapplication": {
    path: path.join(repoRoot, "2-addons", "manifests", "crossplane", "definitions", "application-xrd.yaml"),
    name: "WebApplication Schema",
    description: "Crossplane XRD for WebApplications"
  },
  "shoulders://schemas/state-store": {
    path: path.join(repoRoot, "2-addons", "manifests", "crossplane", "definitions", "state-store-xrd.yaml"),
    name: "StateStore Schema",
    description: "Crossplane XRD for StateStores"
  },
  "shoulders://schemas/event-stream": {
    path: path.join(repoRoot, "2-addons", "manifests", "crossplane", "definitions", "event-stream-xrd.yaml"),
    name: "EventStream Schema",
    description: "Crossplane XRD for EventStreams"
  },
  "shoulders://examples/workspace": {
    path: path.join(repoRoot, "3-user-space", "team-a", "workspace.yaml"),
    name: "Workspace Example",
    description: "Sample Workspace manifest"
  },
  "shoulders://examples/webapplication": {
    path: path.join(repoRoot, "3-user-space", "team-a", "webapp.yaml"),
    name: "WebApplication Example",
    description: "Sample WebApplication manifest"
  },
  "shoulders://examples/state-store": {
    path: path.join(repoRoot, "3-user-space", "team-a", "state-store.yaml"),
    name: "StateStore Example",
    description: "Sample StateStore manifest"
  },
  "shoulders://examples/event-stream": {
    path: path.join(repoRoot, "3-user-space", "team-a", "event-stream.yaml"),
    name: "EventStream Example",
    description: "Sample EventStream manifest"
  }
};

const server = new Server(
  {
    name: "shoulders-mcp-server",
    version: "0.2.0"
  },
  {
    capabilities: {
      tools: {},
      resources: {}
    }
  }
);

server.setRequestHandler(ListToolsRequestSchema, async () => {
  return {
    tools: [
      {
        name: "create_workspace",
        description: "Create a new workspace",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Workspace name" }
          },
          required: ["name"]
        }
      },
      {
        name: "list_workspaces",
        description: "List all workspaces",
        inputSchema: {
          type: "object",
          properties: {}
        }
      },
      {
        name: "use_workspace",
        description: "Set the active workspace",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Workspace name" }
          },
          required: ["name"]
        }
      },
      {
        name: "current_workspace",
        description: "Get the active workspace",
        inputSchema: {
          type: "object",
          properties: {}
        }
      },
      {
        name: "delete_workspace",
        description: "Delete a workspace",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Workspace name" }
          },
          required: ["name"]
        }
      },
      {
        name: "deploy_app",
        description: "Deploy a WebApplication",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Application name" },
            namespace: { type: "string", description: "Workspace namespace" },
            image: { type: "string", description: "Container image" },
            tag: { type: "string", description: "Image tag (overrides tag in image)" },
            replicas: { type: "number", description: "Replica count" },
            host: { type: "string", description: "Hostname for routing" },
            port: { type: "number", description: "Service port" }
          },
          required: ["name", "namespace", "image"]
        }
      },
      {
        name: "list_apps",
        description: "List WebApplications in a workspace",
        inputSchema: {
          type: "object",
          properties: {
            namespace: { type: "string", description: "Workspace namespace (optional if a current workspace is set)" }
          }
        }
      },
      {
        name: "get_app_status",
        description: "Fetch a WebApplication manifest and status",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Application name" },
            namespace: { type: "string", description: "Workspace namespace" }
          },
          required: ["name", "namespace"]
        }
      },
      {
        name: "delete_app",
        description: "Delete a WebApplication",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Application name" },
            namespace: { type: "string", description: "Workspace namespace" }
          },
          required: ["name", "namespace"]
        }
      },
      {
        name: "add_database",
        description: "Provision a StateStore",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "StateStore name" },
            namespace: { type: "string", description: "Workspace namespace" },
            type: { type: "string", enum: ["postgres", "postgresql", "redis"], description: "Database type" },
            tier: { type: "string", enum: ["dev", "prod"], description: "Database tier" }
          },
          required: ["name", "namespace"]
        }
      },
      {
        name: "add_stream",
        description: "Provision an EventStream",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "EventStream name" },
            namespace: { type: "string", description: "Workspace namespace" },
            topics: { type: "array", items: { type: "string" }, description: "Topic names" },
            partitions: { type: "number", description: "Partitions per topic" },
            replicas: { type: "number", description: "Replicas per topic" },
            config: { type: "object", additionalProperties: { type: "string" }, description: "Topic config entries" }
          },
          required: ["name", "namespace", "topics"]
        }
      },
      {
        name: "list_infra",
        description: "List StateStore and EventStream resources",
        inputSchema: {
          type: "object",
          properties: {
            namespace: { type: "string", description: "Workspace namespace (optional if a current workspace is set)" }
          }
        }
      },
      {
        name: "delete_infra",
        description: "Delete a StateStore or EventStream",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Resource name" },
            namespace: { type: "string", description: "Workspace namespace" }
          },
          required: ["name", "namespace"]
        }
      },
      {
        name: "get_platform_status",
        description: "Get cluster/platform status (Flux, Crossplane, Gateway)",
        inputSchema: {
          type: "object",
          properties: {}
        }
      },
      {
        name: "list_clusters",
        description: "List local kind clusters",
        inputSchema: {
          type: "object",
          properties: {}
        }
      },
      {
        name: "use_cluster",
        description: "Switch kube context to a local kind cluster",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Cluster name" }
          },
          required: ["name"]
        }
      },
      {
        name: "get_app_logs",
        description: "Fetch recent application logs via Loki (fallback to pod logs)",
        inputSchema: {
          type: "object",
          properties: {
            name: { type: "string", description: "Application name" },
            namespace: { type: "string", description: "Workspace namespace (required for CLI fallback)" },
            limit: { type: "number", description: "Max log entries to return", default: 200 },
            sinceSeconds: { type: "number", description: "Lookback window in seconds", default: 300 }
          },
          required: ["name"]
        }
      },
      {
        name: "get_trace",
        description: "Fetch a trace by ID from Tempo",
        inputSchema: {
          type: "object",
          properties: {
            traceId: { type: "string", description: "Trace ID" }
          },
          required: ["traceId"]
        }
      }
    ]
  };
});

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  const { name, arguments: args } = request.params;
  try {
    switch (name) {
      case "create_workspace":
        return await createWorkspace(args as unknown as CreateWorkspaceArgs);
      case "list_workspaces":
        return await listWorkspaces();
      case "use_workspace":
        return await useWorkspace(args as unknown as UseWorkspaceArgs);
      case "current_workspace":
        return await currentWorkspace();
      case "delete_workspace":
        return await deleteWorkspace(args as unknown as DeleteWorkspaceArgs);
      case "deploy_app":
        return await deployApp(args as unknown as DeployAppArgs);
      case "list_apps":
        return await listApps(args as unknown as AppListArgs);
      case "get_app_status":
        return await getAppStatus(args as unknown as AppStatusArgs);
      case "delete_app":
        return await deleteApp(args as unknown as AppDeleteArgs);
      case "add_database":
        return await addDatabase(args as unknown as AddDatabaseArgs);
      case "add_stream":
        return await addStream(args as unknown as AddStreamArgs);
      case "list_infra":
        return await listInfra(args as unknown as InfraListArgs);
      case "delete_infra":
        return await deleteInfra(args as unknown as InfraDeleteArgs);
      case "get_platform_status":
        return await getPlatformStatus();
      case "list_clusters":
        return await listClusters();
      case "use_cluster":
        return await useCluster(args as unknown as UseClusterArgs);
      case "get_app_logs":
        return await getAppLogs(args as unknown as AppLogsArgs);
      case "get_trace":
        return await getTrace(args as unknown as TraceArgs);
      default:
        throw new McpError(ErrorCode.MethodNotFound, `Tool ${name} not found`);
    }
  } catch (error) {
    throw mapError(error);
  }
});

server.setRequestHandler(ListResourcesRequestSchema, async () => {
  return {
    resources: Object.entries(resourceIndex)
      .filter(([, entry]) => existsSync(entry.path))
      .map(([uri, entry]) => ({
        uri,
        mimeType: "text/yaml",
        name: entry.name,
        description: entry.description
      }))
  };
});

server.setRequestHandler(ReadResourceRequestSchema, async (request) => {
  const entry = resourceIndex[request.params.uri];
  if (!entry) {
    throw new McpError(ErrorCode.InvalidRequest, `Resource ${request.params.uri} not found`);
  }
  if (!existsSync(entry.path)) {
    throw new McpError(ErrorCode.InvalidRequest, `Resource file missing at ${entry.path}`);
  }
  const text = await readFileText(entry.path);
  return {
    contents: [
      {
        uri: request.params.uri,
        mimeType: "text/yaml",
        text
      }
    ]
  };
});

async function createWorkspace(args: CreateWorkspaceArgs) {
  validateK8sName(args.name, "workspace name");
  const body = {
    apiVersion: `${shouldersGroup}/${shouldersVersion}`,
    kind: "Workspace",
    metadata: { name: args.name },
    spec: {}
  };
  const result = await applyClusterCustomObject("workspaces", body);
  return ok("Workspace created", result.body, { source: "kubernetes", action: result.action });
}

async function listWorkspaces() {
  const { customObjects } = buildKubeClients();
  const result = await customObjects.listClusterCustomObject({
    group: shouldersGroup,
    version: shouldersVersion,
    plural: "workspaces"
  });
  const items = (result as { items?: unknown[] }).items ?? [];
  return ok("Workspaces listed", items, { source: "kubernetes" });
}

async function useWorkspace(args: UseWorkspaceArgs) {
  validateK8sName(args.name, "workspace name");
  const { customObjects } = buildKubeClients();
  await customObjects.getClusterCustomObject({
    group: shouldersGroup,
    version: shouldersVersion,
    plural: "workspaces",
    name: args.name
  });
  await setCurrentWorkspace(args.name);
  return ok("Workspace selected", args.name, { source: "kubernetes" });
}

async function currentWorkspace() {
  const current = await getCurrentWorkspace();
  return ok("Current workspace", current ?? "No workspace selected", { source: "kubernetes" });
}

async function deleteWorkspace(args: DeleteWorkspaceArgs) {
  validateK8sName(args.name, "workspace name");
  const { customObjects } = buildKubeClients();
  await customObjects.deleteClusterCustomObject({
    group: shouldersGroup,
    version: shouldersVersion,
    plural: "workspaces",
    name: args.name
  });
  return ok("Workspace deleted", args.name, { source: "kubernetes" });
}

async function deployApp(args: DeployAppArgs) {
  validateK8sName(args.name, "app name");
  validateK8sName(args.namespace, "namespace");
  validateImage(args.image);

  const { image, tag } = parseImageTag(args.image, args.tag);
  const replicas = typeof args.replicas === "number" ? args.replicas : 1;
  const host = args.host && args.host.trim().length > 0 ? args.host : `${args.name}.local`;
  const port = typeof args.port === "number" ? args.port : 80;
  const annotations = port > 0 ? { "shoulders.io/port": String(port) } : undefined;

  const body = {
    apiVersion: `${shouldersGroup}/${shouldersVersion}`,
    kind: "WebApplication",
    metadata: {
      name: args.name,
      namespace: args.namespace,
      annotations
    },
    spec: {
      image,
      tag,
      replicas,
      host
    }
  };

  const result = await applyNamespacedCustomObject("webapplications", args.namespace, body);
  return ok("Application deployed", result.body, { source: "kubernetes", action: result.action });
}

async function listApps(args: AppListArgs) {
  const namespace = await resolveNamespace(args.namespace);
  const { customObjects } = buildKubeClients();
  const result = await customObjects.listNamespacedCustomObject({
    group: shouldersGroup,
    version: shouldersVersion,
    namespace,
    plural: "webapplications"
  });
  const items = (result as { items?: unknown[] }).items ?? [];
  return ok("Applications listed", items, { source: "kubernetes", namespace });
}

async function getAppStatus(args: AppStatusArgs) {
  validateK8sName(args.name, "app name");
  validateK8sName(args.namespace, "namespace");
  const { customObjects } = buildKubeClients();
  const result = await customObjects.getNamespacedCustomObject({
    group: shouldersGroup,
    version: shouldersVersion,
    namespace: args.namespace,
    plural: "webapplications",
    name: args.name
  });
  return ok("Application status", result, { source: "kubernetes" });
}

async function deleteApp(args: AppDeleteArgs) {
  validateK8sName(args.name, "app name");
  validateK8sName(args.namespace, "namespace");
  const { customObjects } = buildKubeClients();
  await customObjects.deleteNamespacedCustomObject({
    group: shouldersGroup,
    version: shouldersVersion,
    namespace: args.namespace,
    plural: "webapplications",
    name: args.name
  });
  return ok("Application deleted", args.name, { source: "kubernetes" });
}

async function addDatabase(args: AddDatabaseArgs) {
  validateK8sName(args.name, "resource name");
  validateK8sName(args.namespace, "namespace");

  const dbType = args.type ?? "postgres";
  const tier = args.tier ?? "dev";
  const storage = tier === "prod" ? "10Gi" : "1Gi";
  const postgresEnabled = dbType === "postgres" || dbType === "postgresql";
  const redisEnabled = dbType === "redis";
  if (!postgresEnabled && !redisEnabled) {
    throw new ValidationError(`unsupported database type: ${dbType}`);
  }

  const body = {
    apiVersion: `${shouldersGroup}/${shouldersVersion}`,
    kind: "StateStore",
    metadata: { name: args.name, namespace: args.namespace },
    spec: {
      postgresql: {
        enabled: postgresEnabled,
        storage,
        databases: [args.name]
      },
      redis: {
        enabled: redisEnabled,
        replicas: 1
      }
    }
  };

  const result = await applyNamespacedCustomObject("statestores", args.namespace, body);
  return ok("Infrastructure created", result.body, { source: "kubernetes", action: result.action });
}

async function addStream(args: AddStreamArgs) {
  validateK8sName(args.name, "resource name");
  validateK8sName(args.namespace, "namespace");
  if (!Array.isArray(args.topics) || args.topics.length === 0) {
    throw new ValidationError("topics must be a non-empty array");
  }

  const topics = args.topics.map((topic) => topic.trim()).filter(Boolean);
  if (topics.length === 0) {
    throw new ValidationError("topics must include at least one non-empty value");
  }

  const topicSpecs = topics.map((topic) => {
    const spec: Record<string, unknown> = { name: topic };
    if (typeof args.partitions === "number") spec.partitions = args.partitions;
    if (typeof args.replicas === "number") spec.replicas = args.replicas;
    if (args.config && Object.keys(args.config).length > 0) spec.config = args.config;
    return spec;
  });

  const body = {
    apiVersion: `${shouldersGroup}/${shouldersVersion}`,
    kind: "EventStream",
    metadata: { name: args.name, namespace: args.namespace },
    spec: {
      topics: topicSpecs
    }
  };

  const result = await applyNamespacedCustomObject("eventstreams", args.namespace, body);
  return ok("Event stream created", result.body, { source: "kubernetes", action: result.action });
}

async function listInfra(args: InfraListArgs) {
  const namespace = await resolveNamespace(args.namespace);
  const { customObjects } = buildKubeClients();
  const [stores, streams] = await Promise.all([
    customObjects.listNamespacedCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      namespace,
      plural: "statestores"
    }),
    customObjects.listNamespacedCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      namespace,
      plural: "eventstreams"
    })
  ]);
  const items = [
    ...((stores as { items?: unknown[] }).items ?? []),
    ...((streams as { items?: unknown[] }).items ?? [])
  ];
  return ok("Infrastructure listed", items, { source: "kubernetes", namespace });
}

async function deleteInfra(args: InfraDeleteArgs) {
  validateK8sName(args.name, "resource name");
  validateK8sName(args.namespace, "namespace");
  const { customObjects } = buildKubeClients();
  let deleted = false;
  const errors: string[] = [];

  try {
    await customObjects.deleteNamespacedCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      namespace: args.namespace,
      plural: "statestores",
      name: args.name
    });
    deleted = true;
  } catch (error) {
    if (!isNotFoundError(error)) {
      errors.push(error instanceof Error ? error.message : String(error));
    }
  }

  try {
    await customObjects.deleteNamespacedCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      namespace: args.namespace,
      plural: "eventstreams",
      name: args.name
    });
    deleted = true;
  } catch (error) {
    if (!isNotFoundError(error)) {
      errors.push(error instanceof Error ? error.message : String(error));
    }
  }

  if (errors.length > 0) {
    throw new ValidationError(`errors deleting resources: ${errors.join("; ")}`);
  }
  if (!deleted) {
    throw new ValidationError(`infrastructure resource ${args.name} not found`);
  }
  return ok("Infrastructure deleted", args.name, { source: "kubernetes" });
}

async function getPlatformStatus() {
  const { coreV1, customObjects, versionApi } = buildKubeClients();
  let k8sVersion = "unknown";
  try {
    const versionInfo = await versionApi.getCode();
    k8sVersion = versionInfo.gitVersion ?? k8sVersion;
  } catch {
    // ignore version errors
  }

  const nodes = await coreV1.listNode({});
  const nodesReady = nodes.items.every((node) => isNodeReady(node.status?.conditions));

  let fluxReady = false;
  let fluxBroken: string[] = [];
  try {
    const fluxList = await customObjects.listNamespacedCustomObject({
      group: "kustomize.toolkit.fluxcd.io",
      version: "v1",
      namespace: "flux-system",
      plural: "kustomizations"
    });
    const items = (fluxList as { items?: Record<string, unknown>[] }).items ?? [];
    fluxBroken = items
      .filter((item) => !hasCondition(item, "Ready", "True"))
      .map((item) => String((item.metadata as { name?: string } | undefined)?.name ?? "unknown"));
    fluxReady = fluxBroken.length === 0;
  } catch (error) {
    fluxReady = false;
    fluxBroken = [error instanceof Error ? error.message : String(error)];
  }

  let xPlaneReady = false;
  let xPlaneBroken: string[] = [];
  try {
    const providers = await customObjects.listClusterCustomObject({
      group: "pkg.crossplane.io",
      version: "v1",
      plural: "providers"
    });
    const items = (providers as { items?: Record<string, unknown>[] }).items ?? [];
    xPlaneBroken = items
      .filter((item) => !hasCondition(item, "Healthy", "True"))
      .map((item) => String((item.metadata as { name?: string } | undefined)?.name ?? "unknown"));
    xPlaneReady = xPlaneBroken.length === 0;
  } catch (error) {
    xPlaneReady = false;
    xPlaneBroken = [error instanceof Error ? error.message : String(error)];
  }

  let gatewayReady = false;
  let gatewayAddr = "Pending";
  try {
    const gateways = await customObjects.listNamespacedCustomObject({
      group: "gateway.networking.k8s.io",
      version: "v1",
      namespace: "gateway",
      plural: "gateways"
    });
    const items = (gateways as { items?: Record<string, unknown>[] }).items ?? [];
    if (items.length > 0) {
      gatewayReady = true;
      const status = items[0].status as { addresses?: Array<{ value?: string }> } | undefined;
      if (status?.addresses && status.addresses.length > 0) {
        gatewayAddr = status.addresses[0].value ?? gatewayAddr;
      }
    }
  } catch {
    gatewayReady = false;
  }

  const summary = {
    k8sVersion,
    nodesReady,
    nodeCount: nodes.items.length,
    fluxReady,
    fluxBroken,
    crossplaneReady: xPlaneReady,
    crossplaneBroken: xPlaneBroken,
    gatewayReady,
    gatewayAddress: gatewayAddr
  };

  return ok("Platform status", summary, { source: "kubernetes" });
}

async function listClusters() {
  const { kubeConfig } = buildKubeClients();
  const clusters = listKindClusters(kubeConfig);
  return ok("Clusters listed", clusters, { source: "kubernetes" });
}

async function useCluster(args: UseClusterArgs) {
  validateK8sName(args.name, "cluster name");
  const kubeconfigPath = resolveKubeconfigPath();
  const contextName = `kind-${args.name}`;
  await setCurrentContext(kubeconfigPath, contextName);
  return ok("Cluster selected", { name: args.name, context: contextName }, { source: "kubernetes" });
}

async function getAppLogs(args: AppLogsArgs) {
  validateK8sName(args.name, "app name");

  const limit = Math.min(Math.max(args.limit ?? 200, 1), 2000);
  const sinceSeconds = Math.min(Math.max(args.sinceSeconds ?? 300, 60), 3600);

  try {
    const logs = await queryLoki(args.name, limit, sinceSeconds);
    return ok("Application logs", logs, { source: "loki" });
  } catch (error) {
    logger.warn("Loki query failed, falling back to CLI", {
      error: error instanceof Error ? error.message : String(error)
    });
  }

  const namespace = await resolveNamespace(args.namespace);
  const { coreV1 } = buildKubeClients();
  const selector = `app=${args.name}`;
  const pods = await coreV1.listNamespacedPod({
    namespace,
    labelSelector: selector
  });
  if (pods.items.length === 0) {
    throw new ValidationError(`no pods found for selector ${selector}`);
  }

  const logs: string[] = [];
  for (const pod of pods.items) {
    const podName = pod.metadata?.name;
    if (!podName) continue;
    const response = await coreV1.readNamespacedPodLog({
      name: podName,
      namespace,
      sinceSeconds,
      tailLines: limit
    });
    const header = `--- pod/${podName} ---`;
    logs.push(header, response);
  }

  return ok("Application logs", { output: truncate(logs.join("\n"), 12000) }, { source: "kubernetes" });
}

async function getTrace(args: TraceArgs) {
  if (!args.traceId || args.traceId.trim().length === 0) {
    throw new ValidationError("traceId is required");
  }
  const trace = await queryTempo(args.traceId);
  return ok("Trace fetched", trace, { source: "tempo" });
}

async function queryLoki(appName: string, limit: number, sinceSeconds: number) {
  const { params } = buildLokiQueryParams(appName, limit, sinceSeconds);
  return withPortForward(
    {
      namespace: observabilityNamespace,
      service: lokiService,
      remotePort: lokiRemotePort,
      timeoutMs: portForwardTimeoutMs
    },
    async (localPort) => {
      const url = `http://127.0.0.1:${localPort}/loki/api/v1/query_range?${params.toString()}`;
      return httpGetJson(url);
    }
  );
}

async function queryTempo(traceId: string) {
  return withPortForward(
    {
      namespace: observabilityNamespace,
      service: tempoService,
      remotePort: tempoRemotePort,
      timeoutMs: portForwardTimeoutMs
    },
    async (localPort) => {
      const url = `http://127.0.0.1:${localPort}${buildTempoTracePath(traceId)}`;
      return httpGetJson(url);
    }
  );
}

function ok(message: string, data?: unknown, meta?: Record<string, unknown>) {
  return respond({ ok: true, message, data, meta });
}

async function resolveNamespace(explicit?: string) {
  if (explicit && explicit.trim().length > 0) {
    validateK8sName(explicit, "namespace");
    return explicit;
  }
  const current = await getCurrentWorkspace();
  if (!current) {
    throw new ValidationError("no active workspace: run 'shoulders workspace use <name>' or pass --namespace");
  }
  return current;
}

function parseImageTag(image: string, overrideTag?: string) {
  if (overrideTag && overrideTag.trim().length > 0) {
    return { image, tag: overrideTag };
  }
  const parts = image.split(":");
  if (parts.length === 2) {
    return { image: parts[0], tag: parts[1] };
  }
  return { image, tag: "latest" };
}

function hasCondition(item: Record<string, unknown>, type: string, expected: string): boolean {
  const status = item.status as { conditions?: Array<{ type?: string; status?: string }> } | undefined;
  const conditions = status?.conditions;
  if (!conditions) return false;
  return conditions.some((condition) => condition.type === type && condition.status === expected);
}

function isNodeReady(conditions?: Array<{ type?: string; status?: string }>): boolean {
  if (!conditions) return false;
  return conditions.some((condition) => condition.type === "Ready" && condition.status === "True");
}

async function applyNamespacedCustomObject(
  plural: string,
  namespace: string,
  body: Record<string, unknown>
): Promise<{ action: "created" | "updated"; body: unknown }> {
  const { customObjects } = buildKubeClients();
  try {
    const existing = await customObjects.getNamespacedCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      namespace,
      plural,
      name: String((body.metadata as { name?: string } | undefined)?.name ?? "")
    });
    const resourceVersion = (existing as { metadata?: { resourceVersion?: string } }).metadata?.resourceVersion;
    if (resourceVersion) {
      (body.metadata as { resourceVersion?: string }).resourceVersion = resourceVersion;
    }
    const result = await customObjects.replaceNamespacedCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      namespace,
      plural,
      name: String((body.metadata as { name?: string } | undefined)?.name ?? ""),
      body
    });
    return { action: "updated", body: result };
  } catch (error) {
    if (!isNotFoundError(error)) {
      throw error;
    }
    const result = await customObjects.createNamespacedCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      namespace,
      plural,
      body
    });
    return { action: "created", body: result };
  }
}

async function applyClusterCustomObject(
  plural: string,
  body: Record<string, unknown>
): Promise<{ action: "created" | "updated"; body: unknown }> {
  const { customObjects } = buildKubeClients();
  try {
    const existing = await customObjects.getClusterCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      plural,
      name: String((body.metadata as { name?: string } | undefined)?.name ?? "")
    });
    const resourceVersion = (existing as { metadata?: { resourceVersion?: string } }).metadata?.resourceVersion;
    if (resourceVersion) {
      (body.metadata as { resourceVersion?: string }).resourceVersion = resourceVersion;
    }
    const result = await customObjects.replaceClusterCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      plural,
      name: String((body.metadata as { name?: string } | undefined)?.name ?? ""),
      body
    });
    return { action: "updated", body: result };
  } catch (error) {
    if (!isNotFoundError(error)) {
      throw error;
    }
    const result = await customObjects.createClusterCustomObject({
      group: shouldersGroup,
      version: shouldersVersion,
      plural,
      body
    });
    return { action: "created", body: result };
  }
}

function respond(payload: { ok: boolean; message: string; data?: unknown; meta?: Record<string, unknown>; warnings?: string[] }) {
  return {
    content: [
      {
        type: "text",
        text: JSON.stringify(payload, null, 2)
      }
    ]
  };
}

function mapError(error: unknown): McpError {
  if (error instanceof ValidationError) {
    return new McpError(ErrorCode.InvalidParams, error.message);
  }
  if (error instanceof PortForwardError) {
    return new McpError(
      ErrorCode.InternalError,
      `${error.message}. Hint: ensure kubectl is installed and the cluster is reachable.`
    );
  }
  if (error instanceof HttpError) {
    return new McpError(ErrorCode.InternalError, error.message);
  }
  const kubeError = normalizeKubeError(error);
  if (kubeError) {
    if (kubeError.statusCode === 404) {
      return new McpError(ErrorCode.InvalidRequest, kubeError.message);
    }
    if (kubeError.statusCode === 400) {
      return new McpError(ErrorCode.InvalidParams, kubeError.message);
    }
    if (kubeError.statusCode === 409) {
      return new McpError(ErrorCode.InvalidRequest, kubeError.message);
    }
    return new McpError(ErrorCode.InternalError, kubeError.message);
  }
  if (error instanceof McpError) {
    return error;
  }
  return new McpError(
    ErrorCode.InternalError,
    `Unhandled error: ${error instanceof Error ? error.message : String(error)}`
  );
}

function normalizeKubeError(error: unknown): { statusCode?: number; message: string } | null {
  const err = error as { statusCode?: number; response?: { statusCode?: number }; body?: { message?: string }; message?: string };
  const statusCode = err?.statusCode ?? err?.response?.statusCode;
  const message = err?.body?.message || err?.message;
  if (!statusCode && !message) {
    return null;
  }
  return { statusCode, message: message ?? "Kubernetes API error" };
}

async function main() {
  const transport = new StdioServerTransport();
  await server.connect(transport);
  logger.info("Shoulders MCP server running on stdio", { repoRoot });
  await new Promise(() => {});
}

const shutdown = async () => {
  logger.info("Shutting down MCP server");
  process.exit(0);
};

process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);

main().catch((error) => {
  logger.error("Failed to start MCP server", { error: error instanceof Error ? error.message : String(error) });
  process.exit(1);
});
