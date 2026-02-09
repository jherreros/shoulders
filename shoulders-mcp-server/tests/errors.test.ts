import assert from "node:assert/strict";
import { test } from "node:test";

import { HttpError, PortForwardError, ValidationError } from "../src/errors.js";

test("ValidationError sets name and message", () => {
  const err = new ValidationError("invalid input");
  assert.equal(err.name, "ValidationError");
  assert.equal(err.message, "invalid input");
  assert.ok(err instanceof Error);
});

test("PortForwardError sets name and message", () => {
  const err = new PortForwardError("connection refused");
  assert.equal(err.name, "PortForwardError");
  assert.equal(err.message, "connection refused");
});

test("HttpError sets status and statusText", () => {
  const err = new HttpError("Not Found", 404, "Not Found");
  assert.equal(err.name, "HttpError");
  assert.equal(err.message, "Not Found");
  assert.equal(err.status, 404);
  assert.equal(err.statusText, "Not Found");
});
