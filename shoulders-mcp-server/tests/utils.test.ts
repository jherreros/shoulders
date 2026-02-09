import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import path from "node:path";
import os from "node:os";

import { safeJsonParse, truncate, validateK8sName, validateImage, execCommand, getFreePort, resolveRepoRoot } from "../src/utils.js";

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


