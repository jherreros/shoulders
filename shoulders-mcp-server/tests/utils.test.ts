import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import path from "node:path";
import os from "node:os";

import {
  safeJsonParse, truncate, validateK8sName, validateImage, execCommand, getFreePort, resolveRepoRoot,
  contextNameForCluster, resolveTargetCluster, loadKubeConfig
} from "../src/utils.js";

test("validateK8sName accepts valid names", () => {
  assert.doesNotThrow(() => validateK8sName("team-a", "workspace"));
  assert.doesNotThrow(() => validateK8sName("a", "workspace"));
});

test("validateK8sName rejects invalid names", () => {
  assert.throws(() => validateK8sName("Team-A", "workspace"));
  assert.throws(() => validateK8sName("-bad", "workspace"));
  assert.throws(() => validateK8sName("bad-", "workspace"));
});

test("safeJsonParse parses valid JSON", () => {
  const value = safeJsonParse<{ ok: boolean }>("{\"ok\":true}");
  assert.equal(value?.ok, true);
});

test("safeJsonParse returns null for invalid JSON", () => {
  const value = safeJsonParse("not-json");
  assert.equal(value, null);
});

test("truncate short strings", () => {
  const input = "short";
  assert.equal(truncate(input, 10), input);
});

test("truncate long strings", () => {
  const input = "x".repeat(20);
  const output = truncate(input, 10);
  assert.ok(output.startsWith("x".repeat(10)));
  assert.ok(output.includes("truncated"));
});

test("validateImage accepts valid images", () => {
  assert.doesNotThrow(() => validateImage("nginx"));
  assert.doesNotThrow(() => validateImage("nginx:latest"));
  assert.doesNotThrow(() => validateImage("registry.example.com/my/image:v1"));
});

test("validateImage rejects empty images", () => {
  assert.throws(() => validateImage(""));
  assert.throws(() => validateImage("   "));
});

test("execCommand runs shell commands", async () => {
  const result = await execCommand("echo", ["hello"], {});
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout.trim(), "hello");
  assert.equal(result.timedOut, false);
});

test("execCommand handles non-zero exit code", async () => {
  // ls of non-existent file
  const result = await execCommand("ls", ["non-existent-file-xyz"], {});
  assert.notEqual(result.exitCode, 0);
  assert.ok(result.stderr.length > 0);
});

test("getFreePort returns a port number", async () => {
  const port = await getFreePort();
  assert.ok(typeof port === "number");
  assert.ok(port > 0);
  assert.ok(port < 65536);
});

test("resolveRepoRoot finds root with ROADMAP.md and shoulders-cli", async () => {
  const tmpDir = await mkdtemp(path.join(os.tmpdir(), "shoulders-test-"));
  try {
    // Create markers
    await writeFile(path.join(tmpDir, "ROADMAP.md"), "test");
    await mkdir(path.join(tmpDir, "shoulders-cli"));

    // Create nested dir
    const nested = path.join(tmpDir, "subdir", "deep");
    await mkdir(nested, { recursive: true });

    const root = resolveRepoRoot(nested);
    // On mac /var/folders/... is sometimes resolved to /private/var/folders/...
    // so we compare relative path or normalized names
    assert.ok(root.endsWith(path.basename(tmpDir)) || root.includes(path.basename(tmpDir)));
  } finally {
    await rm(tmpDir, { recursive: true, force: true });
  }
});



// --- Cluster resolution (D12) -------------------------------------------------
//
// The server used to always follow the kubeconfig's current-context. That is
// only correct when exactly one cluster exists; with a topology it silently
// addresses whichever cluster was selected last.

const kubeconfigFixture = `
apiVersion: v1
kind: Config
current-context: vcluster-docker_hub
clusters:
  - name: vcluster-docker_hub
    cluster: { server: https://hub.example:8443 }
  - name: vcluster-docker_spoke-a
    cluster: { server: https://spoke-a.example:8443 }
contexts:
  - name: vcluster-docker_hub
    context: { cluster: vcluster-docker_hub, user: hub }
  - name: vcluster-docker_spoke-a
    context: { cluster: vcluster-docker_spoke-a, user: spoke-a }
users:
  - name: hub
    user: { token: t }
  - name: spoke-a
    user: { token: t }
`;

async function withKubeconfig(body: (dir: string, file: string) => Promise<void>): Promise<void> {
  const dir = await mkdtemp(path.join(os.tmpdir(), "shoulders-mcp-"));
  const file = path.join(dir, "config");
  await writeFile(file, kubeconfigFixture, "utf-8");
  const previousKubeconfig = process.env.KUBECONFIG;
  const previousCluster = process.env.SHOULDERS_CLUSTER;
  process.env.KUBECONFIG = file;
  delete process.env.SHOULDERS_CLUSTER;
  try {
    await body(dir, file);
  } finally {
    if (previousKubeconfig === undefined) delete process.env.KUBECONFIG;
    else process.env.KUBECONFIG = previousKubeconfig;
    if (previousCluster === undefined) delete process.env.SHOULDERS_CLUSTER;
    else process.env.SHOULDERS_CLUSTER = previousCluster;
    await rm(dir, { recursive: true, force: true });
  }
}

test("contextNameForCluster adds the vind prefix but is idempotent", () => {
  assert.equal(contextNameForCluster("hub"), "vcluster-docker_hub");
  assert.equal(contextNameForCluster("vcluster-docker_hub"), "vcluster-docker_hub");
});

test("resolveTargetCluster reads SHOULDERS_CLUSTER and ignores blanks", () => {
  delete process.env.SHOULDERS_CLUSTER;
  assert.equal(resolveTargetCluster(), null);
  process.env.SHOULDERS_CLUSTER = "   ";
  assert.equal(resolveTargetCluster(), null);
  process.env.SHOULDERS_CLUSTER = "  spoke-a  ";
  assert.equal(resolveTargetCluster(), "spoke-a");
  delete process.env.SHOULDERS_CLUSTER;
});

test("loadKubeConfig follows the current-context when no cluster is requested", async () => {
  await withKubeconfig(async () => {
    const { kubeConfig } = loadKubeConfig();
    assert.equal(kubeConfig.getCurrentContext(), "vcluster-docker_hub");
  });
});

test("loadKubeConfig honours an explicit cluster over the current-context", async () => {
  await withKubeconfig(async () => {
    const { kubeConfig } = loadKubeConfig("spoke-a");
    assert.equal(kubeConfig.getCurrentContext(), "vcluster-docker_spoke-a");
  });
});

test("loadKubeConfig honours SHOULDERS_CLUSTER", async () => {
  await withKubeconfig(async () => {
    process.env.SHOULDERS_CLUSTER = "spoke-a";
    const { kubeConfig } = loadKubeConfig();
    assert.equal(kubeConfig.getCurrentContext(), "vcluster-docker_spoke-a");
  });
});

test("loadKubeConfig fails loudly for an unknown cluster rather than falling back", async () => {
  await withKubeconfig(async () => {
    // A silent fallback here would answer questions about spoke-a with the
    // hub's data, which is worse than an error.
    assert.throws(
      () => loadKubeConfig("nope"),
      (error: Error) => {
        assert.match(error.message, /nope/);
        assert.match(error.message, /hub, spoke-a/);
        return true;
      }
    );
  });
});
