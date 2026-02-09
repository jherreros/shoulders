import test from "node:test";
import assert from "node:assert/strict";
import { createApp } from "../server.js";

process.env.SHOULDERS_MOCK = "1";

function startServer() {
  const app = createApp();
  return new Promise((resolve) => {
    const server = app.listen(0, () => {
      const { port } = server.address();
      resolve({ server, baseUrl: `http://127.0.0.1:${port}` });
    });
  });
}

test("summary endpoint returns mock data", async () => {
  const { server, baseUrl } = await startServer();
  const response = await fetch(`${baseUrl}/api/summary`);
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.counts.workspaces, 2);
  assert.ok(Array.isArray(body.resources.webApplications));
  server.close();
});

test("contexts endpoint returns current context", async () => {
  const { server, baseUrl } = await startServer();
  const response = await fetch(`${baseUrl}/api/contexts`);
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.current, "kind-shoulders");
  assert.ok(body.contexts.includes("prod-cluster"));
  server.close();
});

test("apply endpoint parses YAML", async () => {
  const { server, baseUrl } = await startServer();
  const yaml = `apiVersion: shoulders.io/v1alpha1\nkind: Workspace\nmetadata:\n  name: demo\nspec: {}`;
  const response = await fetch(`${baseUrl}/api/apply`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ yaml }),
  });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.applied[0].kind, "Workspace");
  assert.equal(body.applied[0].name, "demo");
  server.close();
});
