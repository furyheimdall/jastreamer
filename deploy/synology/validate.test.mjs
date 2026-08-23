import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

const root = new URL("../../", import.meta.url).pathname;
const componentctl = join(root, "tooling/componentctl");
const fixture = (name) => join(root, "tooling/fixtures/synology", name);

const run = (...args) => spawnSync(componentctl, ["synology", ...args], {
  cwd: root,
  encoding: "utf8",
});

test("validates the static DS918+ contract and writes redacted evidence", async () => {
  const directory = await mkdtemp(join(tmpdir(), "jstreamer-synology-"));
  try {
    const evidence = join(directory, "contract.json");
    const result = run("validate", "--fixture", fixture("ds918plus.json"), "--evidence", evidence);
    assert.equal(result.status, 0, result.stderr);
    const value = JSON.parse(await readFile(evidence, "utf8"));
    assert.deepEqual(value.required_external_services, ["github"]);
    assert.equal(value.runtime_certification, "candidate-pending-runtime-authorization");
    assert.equal(value.network_identifiers, "REDACTED");
    assert.doesNotMatch(JSON.stringify(value), /192\.168\.|"password"\s*:|"credential"\s*:/i);
    assert.equal(value.credentials_retained, false);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("rejects an ambiguous advertised address without mutation", () => {
  const result = run(
    "validate",
    "--fixture",
    fixture("multi-interface-missing-advertised-address.json"),
  );
  assert.equal(result.status, 78, result.stderr);
  assert.match(result.stderr, /AMBIGUOUS_ADVERTISED_ADDRESS/);
});

test("rejects secrets architecture pins and privileged compose input", () => {
  const result = run(
    "validate",
    "--fixture",
    fixture("ds918plus.json"),
    "--compose",
    fixture("invalid-privileged-compose.yaml"),
  );
  assert.equal(result.status, 65, result.stderr);
  assert.match(result.stderr, /PRIVILEGED_CONTAINER/);
  assert.match(result.stderr, /ARCHITECTURE_PIN/);
  assert.match(result.stderr, /EMBEDDED_SECRET/);
});

test("requires static probe mode to remain read-only and redacted", () => {
  const result = run("probe-readonly", "--host-env", "MISSING_DSM_HOST", "--redact", "--output", "/tmp/unused-probe.json");
  assert.equal(result.status, 64, result.stderr);
  assert.match(result.stderr, /MISSING_HOST_ENV/);
});
