import { AdapterRuntime } from "./adapters";
import { validateCells } from "./matrix";
import {
  CompatibilityError,
  parseArtifact,
} from "./parser";
import type {
  Artifact,
  Cell,
  Matrix,
  Result,
} from "./parser";
import { decodeWire } from "./wire";

const requiredPeerComponents = new Map<
  string,
  Artifact["component"]
>([
  ["server-old", "server"],
  ["server-new", "server"],
  ["renderer-old", "renderer"],
  ["renderer-candidate", "renderer"],
  ["control-old", "control"],
  ["control-current", "control"],
]);

const loadArtifacts = (
  fixtureRoot: string,
  matrix: Matrix,
): ReadonlyMap<string, Artifact> => {
  const references = new Map(matrix.peers.map((reference) => [
    reference.id,
    reference,
  ]));
  if (
    references.size !== requiredPeerComponents.size ||
    [...requiredPeerComponents].some(([id]) => !references.has(id))
  )
    throw new CompatibilityError("exact peer set required");
  const artifacts = new Map(
    [...references].map(([id, reference]) => [
      id,
      parseArtifact(fixtureRoot, reference),
    ]),
  );
  for (const [id, component] of requiredPeerComponents) {
    if (artifacts.get(id)?.component !== component)
      throw new CompatibilityError(`peer component mismatch: ${id}`);
  }
  return artifacts;
};

const selectedMajor = (
  subject: Artifact,
  peer: Artifact,
): number | undefined =>
  subject.supportedMajors
    .filter((major) => peer.supportedMajors.includes(major))
    .sort((left, right) => right - left)[0];

const resultFor = (
  cell: Cell,
  subject: Artifact,
  peer: Artifact,
  subjectHash: string,
  peerHash: string,
  major: number | undefined,
  capabilities: readonly string[],
  adapter: ReturnType<AdapterRuntime["execute"]>,
): Result => {
  const shared = {
    id: cell.id,
    peerVersions: {
      subject: subject.version,
      peer: peer.version,
    },
    rawHashes: {
      subject: subjectHash,
      peer: peerHash,
    },
    candidateSha256: adapter.candidateSha256,
    capabilities,
    trace: adapter.trace,
    adapter: subject.adapter,
    assertions: adapter.assertions,
  };
  if (major === undefined)
    return {
      ...shared,
      status: "failed",
      reason: "MISSING_N_MINUS_ONE",
    };
  return {
    ...shared,
    status: "passed",
    negotiatedMajor: major,
  };
};

export const runMatrix = (
  workspaceRoot: string,
  fixtureRoot: string,
  matrix: Matrix,
): readonly Result[] => {
  validateCells(matrix.cells);
  const artifacts = loadArtifacts(fixtureRoot, matrix);
  const references = new Map(
    matrix.peers.map((reference) => [reference.id, reference]),
  );
  const wires = new Map(
    matrix.wirePayloads.map((reference) => [
      reference.id,
      {
        reference,
        decoded: decodeWire(fixtureRoot, reference),
      },
    ]),
  );
  const runtime = AdapterRuntime.prepare(workspaceRoot, fixtureRoot);
  try {
    return matrix.cells.map((cell) => {
      const subject = artifacts.get(cell.subject);
      const peer = artifacts.get(cell.peer);
      const wire = wires.get(cell.wirePayload);
      const subjectReference = references.get(cell.subject);
      const peerReference = references.get(cell.peer);
      if (
        !subject ||
        !peer ||
        !wire ||
        !subjectReference ||
        !peerReference
      )
        throw new CompatibilityError(
          `cell references unknown peer or wire: ${cell.id}`,
        );
      if (
        wire.reference.consumer !==
        (cell.requiredCapability === "control-api"
          ? "control"
          : "renderer")
      )
        throw new CompatibilityError(`wire consumer mismatch: ${cell.id}`);
      const major = selectedMajor(subject, peer);
      const capabilities = subject.capabilities.filter((capability) =>
        peer.capabilities.includes(capability),
      );
      if (
        major !== undefined &&
        (!capabilities.includes(cell.requiredCapability) ||
          wire.decoded.major !== major)
      )
        throw new CompatibilityError(
          `negotiated capability or wire mismatch: ${cell.id}`,
        );
      const adapter = runtime.execute(
        cell,
        subject,
        peer,
        wire.reference.file,
        major,
      );
      if (
        (major === undefined && adapter.status !== "unsupported") ||
        (major !== undefined && adapter.status !== "passed")
      )
        throw new CompatibilityError(`adapter outcome mismatch: ${cell.id}`);
      return resultFor(
        cell,
        subject,
        peer,
        subjectReference.sha256,
        peerReference.sha256,
        major,
        capabilities,
        adapter,
      );
    });
  } finally {
    runtime.dispose();
  }
};
