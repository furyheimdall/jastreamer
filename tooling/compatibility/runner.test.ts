import { createHash } from "node:crypto";
import { expect, test } from "bun:test";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  parseMatrix,
  read,
} from "./parser";
import type { Matrix } from "./parser";

const sourceRoot = "tooling/fixtures/compatibility";

const withWorkspace = (testBody: (root: string) => void): void => {
  const root = mkdtempSync(join(tmpdir(), "jastreamer-task15-"));
  try {
    testBody(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
};

const writeMatrix = (
  path: string,
  matrix: Matrix,
): void => {
  writeFileSync(path, `${JSON.stringify(matrix, null, 2)}\n`);
};

const copyFixture = (
  root: string,
): { readonly path: string; readonly matrix: Matrix } => {
  const destination = join(root, "tooling/fixtures/compatibility");
  mkdirSync(destination, { recursive: true });
  const path = join(destination, "matrix.json");
  const matrix = parseMatrix(
    read(`${sourceRoot}/released-peers.yaml`),
  );
  writeMatrix(path, matrix);
  for (const peer of matrix.peers)
    writeFileSync(
      join(destination, peer.artifact),
      readFileSync(join(sourceRoot, peer.artifact)),
    );
  for (const wire of matrix.wirePayloads)
    writeFileSync(
      join(destination, wire.file),
      readFileSync(join(sourceRoot, wire.file)),
    );
  return { path, matrix };
};

const run = (path: string, root: string): number =>
  Bun.spawnSync([
    "./tooling/componentctl",
    "compatibility",
    "run",
    "--matrix",
    path,
    "--output",
    join(root, "result.json"),
  ]).exitCode;

const sha = (value: string): string =>
  createHash("sha256").update(value).digest("hex");

test("reports unexpected fixture read failures without masking the cause", () =>
  withWorkspace((root) => {
    const missing = join(root, "missing-matrix.json");
    const result = Bun.spawnSync([
      "./tooling/componentctl",
      "compatibility",
      "run",
      "--matrix",
      missing,
      "--output",
      join(root, "result.json"),
    ], {
      stdout: "pipe",
      stderr: "pipe",
    });
    expect(result.exitCode).toBe(65);
    expect(result.stderr.toString()).toContain("ENOENT");
    expect(result.stderr.toString()).toContain(missing);
  }));

test("rejects mutated peer digest before candidate builds", () =>
  withWorkspace((root) => {
    const fixture = copyFixture(root);
    const peers = fixture.matrix.peers.map((peer, index) =>
      index === 0 ? { ...peer, sha256: "0".repeat(64) } : peer,
    );
    writeMatrix(fixture.path, { ...fixture.matrix, peers });
    expect(run(fixture.path, root)).toBe(65);
  }));

test("rejects missing, duplicate, and mislabeled matrix cells", () =>
  withWorkspace((root) => {
    const fixture = copyFixture(root);
    const cells = fixture.matrix.cells.slice(1);
    writeMatrix(fixture.path, { ...fixture.matrix, cells });
    expect(run(fixture.path, root)).toBe(65);

    const duplicate = [
      ...fixture.matrix.cells.slice(0, -1),
      fixture.matrix.cells[0],
    ];
    writeMatrix(fixture.path, {
      ...fixture.matrix,
      cells: duplicate,
    });
    expect(run(fixture.path, root)).toBe(65);

    const mislabeled = fixture.matrix.cells.map((cell, index) =>
      index === 0 ? { ...cell, peer: "server-new" } : cell,
    );
    writeMatrix(fixture.path, {
      ...fixture.matrix,
      cells: mislabeled,
    });
    expect(run(fixture.path, root)).toBe(65);
  }));

test("rejects rebuilt artifact even when metadata is unchanged", () =>
  withWorkspace((root) => {
    const fixture = copyFixture(root);
    const peer = fixture.matrix.peers[0];
    if (!peer) throw new Error("fixture has no peer");
    writeFileSync(
      join(root, "tooling/fixtures/compatibility", peer.artifact),
      "rebuilt HEAD",
    );
    expect(run(fixture.path, root)).toBe(65);
  }));

test("rejects a breaking required-field fixture with a valid new digest", () =>
  withWorkspace((root) => {
    const fixture = copyFixture(root);
    const reference = fixture.matrix.wirePayloads.find(
      (wire) => wire.id === "control-v1",
    );
    if (!reference) throw new Error("control-v1 wire is absent");
    const wirePath = join(
      root,
      "tooling/fixtures/compatibility",
      reference.file,
    );
    const original = readFileSync(wirePath, "utf8");
    const broken = original.replace(
      /^\s*"requestId":\s*"[^"]+",?\n/m,
      "",
    );
    if (broken === original)
      throw new Error("requestId mutation did not apply");
    writeFileSync(wirePath, broken);
    const wirePayloads = fixture.matrix.wirePayloads.map((wire) =>
      wire.id === reference.id
        ? { ...wire, sha256: sha(broken) }
        : wire,
    );
    writeMatrix(fixture.path, {
      ...fixture.matrix,
      wirePayloads,
    });
    expect(run(fixture.path, root)).toBe(65);
  }));

test("rejects released artifacts that point back to a build command", () =>
  withWorkspace((root) => {
    const fixture = copyFixture(root);
    const reference = fixture.matrix.peers.find(
      (peer) => peer.id === "control-old",
    );
    if (!reference) throw new Error("control-old peer is absent");
    const artifactPath = join(
      root,
      "tooling/fixtures/compatibility",
      reference.artifact,
    );
    const original = readFileSync(artifactPath, "utf8");
    const rebuilt = original.replace(
      "fixture:control-v1",
      "build:workspace-HEAD",
    );
    if (rebuilt === original)
      throw new Error("adapter mutation did not apply");
    writeFileSync(artifactPath, rebuilt);
    const peers = fixture.matrix.peers.map((peer) =>
      peer.id === reference.id
        ? { ...peer, sha256: sha(rebuilt) }
        : peer,
    );
    writeMatrix(fixture.path, { ...fixture.matrix, peers });
    expect(run(fixture.path, root)).toBe(65);
  }));
