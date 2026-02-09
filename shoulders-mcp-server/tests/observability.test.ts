import assert from "node:assert/strict";
import { test } from "node:test";

import { buildLokiQueryParams, buildTempoTracePath } from "../src/observability.js";

test("buildLokiQueryParams builds forward range", () => {
  const now = 1_700_000_000_000; // fixed ms
  const { params, startNs, endNs } = buildLokiQueryParams("api", 100, 300, now);
  assert.ok(startNs < endNs);
  assert.equal(params.get("limit"), "100");
  assert.equal(params.get("direction"), "BACKWARD");
  assert.ok(params.get("query")?.includes("api"));
});

test("buildTempoTracePath formats trace path", () => {
  assert.equal(buildTempoTracePath("abc123"), "/api/traces/abc123");
});
