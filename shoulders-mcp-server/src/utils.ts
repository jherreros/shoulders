import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import os from "node:os";
import path from "node:path";
import readline from "node:readline";
import { fileURLToPath } from "node:url";

import { CoreV1Api, CustomObjectsApi, KubeConfig, VersionApi } from "@kubernetes/client-node";
import YAML from "yaml";

import { HttpError, PortForwardError, ValidationError } from "./errors.js";
import { logger } from "./logger.js";

export interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number | null;
  timedOut: boolean;
}

export interface ExecOptions {
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  timeoutMs?: number;
}

export function resolveRepoRoot(startDir?: string): string {
  const override = process.env.SHOULDERS_REPO_ROOT;
  if (override && override.trim().length > 0 && existsSync(override)) {
    return override;
  }
  let dir = startDir ?? path.dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 6; i += 1) {
    const roadmap = path.join(dir, "ROADMAP.md");
    const cliDir = path.join(dir, "shoulders-cli");
    if (existsSync(roadmap) && existsSync(cliDir)) {
      return dir;
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      break;
    }
    dir = parent;
  }
  return process.cwd();
}

export interface WorkspaceConfig {
  current_workspace?: string;
}

export function resolveWorkspaceConfigPath(): string {
  return path.join(os.homedir(), ".shoulders", "config.yaml");
}

export async function readWorkspaceConfig(): Promise<WorkspaceConfig> {
  const configPath = resolveWorkspaceConfigPath();
  if (!existsSync(configPath)) {
    return {};
  }
  const text = await readFile(configPath, "utf-8");
  try {
    const parsed = YAML.parse(text) as WorkspaceConfig | null;
    if (!parsed || typeof parsed !== "object") {
      return {};
    }
    return parsed;
  } catch {
    return {};
  }
}

export async function writeWorkspaceConfig(config: WorkspaceConfig): Promise<void> {
  const configPath = resolveWorkspaceConfigPath();
  await mkdir(path.dirname(configPath), { recursive: true, mode: 0o755 });
  const text = YAML.stringify(config);
  await writeFile(configPath, text, "utf-8");
}

export async function getCurrentWorkspace(): Promise<string | null> {
  const config = await readWorkspaceConfig();
  if (config.current_workspace && config.current_workspace.trim().length > 0) {
    return config.current_workspace.trim();
  }
  return null;
}

export async function setCurrentWorkspace(name: string): Promise<void> {
  const config = await readWorkspaceConfig();
  config.current_workspace = name;
  await writeWorkspaceConfig(config);
}

export function resolveKubeconfigPath(): string {
  const envPath = process.env.KUBECONFIG;
  if (envPath && envPath.trim().length > 0) {
    return envPath.split(path.delimiter)[0];
  }
  return path.join(os.homedir(), ".kube", "config");
}

export function loadKubeConfig(): { kubeConfig: KubeConfig; kubeconfigPath: string } {
  const kubeconfigPath = resolveKubeconfigPath();
  const kubeConfig = new KubeConfig();
  if (existsSync(kubeconfigPath)) {
    kubeConfig.loadFromFile(kubeconfigPath);
  } else {
    kubeConfig.loadFromDefault();
  }
  return { kubeConfig, kubeconfigPath };
}

export function buildKubeClients() {
  const { kubeConfig, kubeconfigPath } = loadKubeConfig();
  return {
    kubeConfig,
    kubeconfigPath,
    coreV1: kubeConfig.makeApiClient(CoreV1Api),
    customObjects: kubeConfig.makeApiClient(CustomObjectsApi),
    versionApi: kubeConfig.makeApiClient(VersionApi)
  };
}

export function listKindClusters(kubeConfig: KubeConfig): string[] {
  return kubeConfig
    .getContexts()
    .map((ctx) => ctx.name)
    .filter((name) => name.startsWith("kind-"))
    .map((name) => name.replace(/^kind-/, ""))
    .sort();
}

export async function setCurrentContext(kubeconfigPath: string, contextName: string): Promise<void> {
  const kubeConfig = new KubeConfig();
  if (existsSync(kubeconfigPath)) {
    kubeConfig.loadFromFile(kubeconfigPath);
  } else {
    kubeConfig.loadFromDefault();
  }
  const contexts = kubeConfig.getContexts().map((ctx) => ctx.name);
  if (!contexts.includes(contextName)) {
    throw new ValidationError(`context ${contextName} not found in kubeconfig`);
  }
  kubeConfig.setCurrentContext(contextName);
  const yamlText = kubeConfig.exportConfig();
  await writeFile(kubeconfigPath, yamlText, "utf-8");
}

export function isNotFoundError(error: unknown): boolean {
  const err = error as { statusCode?: number; response?: { statusCode?: number }; body?: { code?: number } };
  return err?.statusCode === 404 || err?.response?.statusCode === 404 || err?.body?.code === 404;
}

export async function execCommand(command: string, args: string[], options: ExecOptions = {}): Promise<ExecResult> {
  return new Promise((resolve, reject) => {
    const proc = spawn(command, args, {
      cwd: options.cwd,
      env: options.env,
      stdio: ["ignore", "pipe", "pipe"]
    });

    let stdout = "";
    let stderr = "";
    let timedOut = false;
    let timeout: NodeJS.Timeout | undefined;

    if (proc.stdout) {
      proc.stdout.on("data", (chunk) => {
        stdout += chunk.toString();
      });
    }

    if (proc.stderr) {
      proc.stderr.on("data", (chunk) => {
        stderr += chunk.toString();
      });
    }

    if (options.timeoutMs && options.timeoutMs > 0) {
      timeout = setTimeout(() => {
        timedOut = true;
        proc.kill("SIGINT");
        setTimeout(() => proc.kill("SIGKILL"), 2000);
      }, options.timeoutMs);
    }

    proc.on("error", (err) => {
      if (timeout) clearTimeout(timeout);
      reject(err);
    });

    proc.on("close", (code) => {
      if (timeout) clearTimeout(timeout);
      resolve({ stdout, stderr, exitCode: code, timedOut });
    });
  });
}

export function validateK8sName(value: string, label: string): void {
  if (!value || value.trim().length === 0) {
    throw new ValidationError(`${label} is required`);
  }
  if (value.length > 63) {
    throw new ValidationError(`${label} must be 63 characters or fewer`);
  }
  const pattern = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
  if (!pattern.test(value)) {
    throw new ValidationError(`${label} must be a DNS-1123 label (lowercase alphanumeric and '-')`);
  }
}

export function validateImage(value: string): void {
  if (!value || value.trim().length === 0) {
    throw new ValidationError("image is required");
  }
}

export function truncate(value: string, maxLength = 12000): string {
  if (value.length <= maxLength) {
    return value;
  }
  const head = value.slice(0, maxLength);
  return `${head}\n...[truncated ${value.length - maxLength} chars]`;
}

export async function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (typeof address === "object" && address) {
        const port = address.port;
        server.close(() => resolve(port));
      } else {
        server.close();
        reject(new Error("Failed to allocate free port"));
      }
    });
  });
}

export interface PortForwardOptions {
  namespace: string;
  service: string;
  remotePort: number;
  localPort?: number;
  timeoutMs?: number;
}

export async function withPortForward<T>(options: PortForwardOptions, fn: (localPort: number) => Promise<T>): Promise<T> {
  const kubectl = process.env.KUBECTL_BIN || "kubectl";
  const localPort = options.localPort ?? await getFreePort();
  const args = ["-n", options.namespace, "port-forward", `svc/${options.service}`, `${localPort}:${options.remotePort}`];

  logger.debug("Starting kubectl port-forward", { args });
  const proc = spawn(kubectl, args, { stdio: ["ignore", "pipe", "pipe"] });

  try {
    await waitForPortForwardReady(proc, options.timeoutMs ?? 15000);
    return await fn(localPort);
  } finally {
    proc.kill("SIGINT");
    setTimeout(() => proc.kill("SIGKILL"), 2000);
  }
}

async function waitForPortForwardReady(proc: ReturnType<typeof spawn>, timeoutMs: number): Promise<void> {
  return new Promise((resolve, reject) => {
    let settled = false;
    const done = (err?: Error) => {
      if (settled) return;
      settled = true;
      cleanup();
      if (err) reject(err);
      else resolve();
    };

    const onLine = (line: string) => {
      if (line.includes("Forwarding from")) {
        done();
      }
    };

    const rlOut = proc.stdout ? readline.createInterface({ input: proc.stdout }) : null;
    const rlErr = proc.stderr ? readline.createInterface({ input: proc.stderr }) : null;

    rlOut?.on("line", onLine);
    rlErr?.on("line", onLine);

    const timeout = setTimeout(() => {
      done(new PortForwardError("Port-forward timed out"));
    }, timeoutMs);

    const cleanup = () => {
      clearTimeout(timeout);
      rlOut?.close();
      rlErr?.close();
    };

    proc.on("error", (err) => {
      done(new PortForwardError(`kubectl error: ${err.message}`));
    });

    proc.on("exit", (code) => {
      if (!settled) {
        done(new PortForwardError(`kubectl exited with code ${code ?? "unknown"}`));
      }
    });
  });
}

export async function httpGetJson(url: string): Promise<unknown> {
  const response = await fetch(url);
  const body = await response.text();
  if (!response.ok) {
    throw new HttpError(`HTTP ${response.status} ${response.statusText}: ${body}`, response.status, response.statusText);
  }
  try {
    return JSON.parse(body);
  } catch {
    return { raw: body };
  }
}

export function safeJsonParse<T>(value: string): T | null {
  try {
    return JSON.parse(value) as T;
  } catch {
    return null;
  }
}

export async function readFileText(pathname: string): Promise<string> {
  return readFile(pathname, "utf-8");
}
