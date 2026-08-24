import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve, sep } from "node:path";

type RecordValue = { readonly [key: string]: unknown };
export type Artifact = Readonly<{
  id: string;
  component: "server" | "control" | "renderer";
  version: string;
  supportedMajors: readonly number[];
  capabilities: readonly string[];
  adapter: string;
  sentinel: string;
}>;
export type PeerRef = Readonly<{
  id: string;
  artifact: string;
  sha256: string;
}>;
type Order = "old-first" | "new-first";
export type Cell = Readonly<{
  id: string;
  subject: string;
  peer: string;
  order: Order;
  requiredCapability: string;
  wirePayload: string;
}>;
export type WireRef = Readonly<{
  id: string;
  major: number;
  file: string;
  sha256: string;
  consumer: "control" | "renderer";
}>;
export type Matrix = Readonly<{
  peers: readonly PeerRef[];
  cells: readonly Cell[];
  wirePolicy: Readonly<{
    additiveFields: string;
    unknownCapabilities: string;
    unknownEnumValues: string;
  }>;
  wirePayloads: readonly WireRef[];
}>;
export type Result = Readonly<{
  id: string;
  status: "passed" | "failed";
  reason?: string;
  peerVersions: Readonly<Record<string, string>>;
  rawHashes: Readonly<Record<string, string>>;
  candidateSha256: string;
  negotiatedMajor?: number;
  capabilities: readonly string[];
  trace: readonly string[];
  adapter: string;
  assertions: readonly string[];
}>;

export class CompatibilityError extends Error {
  readonly code = "INVALID_FIXTURE";
}
export const isRecord = (value: unknown): value is RecordValue =>
  typeof value === "object" && value !== null && !Array.isArray(value);
export const read = (path: string): unknown =>
  JSON.parse(readFileSync(path, "utf8"));
export const text = (value: unknown, name: string): string => {
  if (typeof value !== "string" || value.length === 0)
    throw new CompatibilityError(`${name} must be a non-empty string`);
  return value;
};
const integerList = (value: unknown, name: string): readonly number[] => {
  if (
    !Array.isArray(value) ||
    value.some((item) => typeof item !== "number" || !Number.isInteger(item))
  )
    throw new CompatibilityError(`${name} must be integer array`);
  return value;
};
export const stringList = (value: unknown, name: string): readonly string[] => {
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string"))
    throw new CompatibilityError(`${name} must be string array`);
  return value;
};
export const sha = (bytes: Uint8Array): string =>
  createHash("sha256").update(bytes).digest("hex");
const fixturePath = (root: string, relative: string, id: string): string => {
  const base = resolve(root);
  const path = resolve(base, relative);
  if (!path.startsWith(`${base}${sep}`))
    throw new CompatibilityError(`fixture path escapes root for ${id}`);
  return path;
};

export const parseArtifact = (root: string, ref: PeerRef): Artifact => {
  const path = fixturePath(root, ref.artifact, ref.id);
  const bytes = readFileSync(path);
  if (sha(bytes) !== ref.sha256)
    throw new CompatibilityError(`artifact digest mismatch for ${ref.id}`);
  const value = read(path);
  if (!isRecord(value))
    throw new CompatibilityError(`${ref.id} artifact is not an object`);
  const component = text(value.component, `${ref.id}.component`);
  if (
    component !== "server" &&
    component !== "control" &&
    component !== "renderer"
  )
    throw new CompatibilityError(`${ref.id}.component invalid`);
  if (text(value.id, `${ref.id}.id`) !== ref.id)
    throw new CompatibilityError(`${ref.id} artifact id mismatch`);
  const version = text(value.version, `${ref.id}.version`);
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version))
    throw new CompatibilityError(`${ref.id}.version invalid`);
  const adapter = text(value.adapter, `${ref.id}.adapter`);
  const expectedAdapters: Readonly<Record<string, string>> = {
    "server-old": "fixture:server-v1",
    "server-new": "candidate:server",
    "control-old": "fixture:control-v1",
    "control-current": "candidate:control",
    "renderer-old": "fixture:renderer-v1",
    "renderer-candidate": "candidate:renderer",
  };
  if (adapter !== expectedAdapters[ref.id])
    throw new CompatibilityError(`${ref.id}.adapter invalid`);
  return {
    id: ref.id,
    component,
    version,
    supportedMajors: integerList(
      value.supportedMajors,
      `${ref.id}.supportedMajors`,
    ),
    capabilities: stringList(value.capabilities, `${ref.id}.capabilities`),
    adapter,
    sentinel: text(value.sentinel, `${ref.id}.sentinel`),
  };
};
export const parseMatrix = (value: unknown): Matrix => {
  if (
    !isRecord(value) ||
    !Array.isArray(value.peers) ||
    !Array.isArray(value.cells) ||
    !Array.isArray(value.wirePayloads) ||
    !isRecord(value.wirePolicy)
  )
    throw new CompatibilityError("matrix boundary fields invalid");
  const peers = value.peers.map((item, index) => {
    if (!isRecord(item))
      throw new CompatibilityError(`peers[${index}] invalid`);
    return {
      id: text(item.id, `peers[${index}].id`),
      artifact: text(item.artifact, `peers[${index}].artifact`),
      sha256: text(item.sha256, `peers[${index}].sha256`),
    };
  });
  const cells = value.cells.map((item, index): Cell => {
    if (!isRecord(item))
      throw new CompatibilityError(`cells[${index}] invalid`);
    const order = text(item.order, `cells[${index}].order`);
    if (order !== "old-first" && order !== "new-first")
      throw new CompatibilityError(`cells[${index}].order invalid`);
    return {
      id: text(item.id, `cells[${index}].id`),
      subject: text(item.subject, `cells[${index}].subject`),
      peer: text(item.peer, `cells[${index}].peer`),
      order,
      requiredCapability: text(
        item.requiredCapability,
        `cells[${index}].requiredCapability`,
      ),
      wirePayload: text(item.wirePayload, `cells[${index}].wirePayload`),
    };
  });
  const wires = value.wirePayloads.map((item, index): WireRef => {
    if (!isRecord(item))
      throw new CompatibilityError(`wirePayloads[${index}] invalid`);
    const consumer = text(item.consumer, `wirePayloads[${index}].consumer`);
    if (consumer !== "control" && consumer !== "renderer")
      throw new CompatibilityError(`wirePayloads[${index}].consumer invalid`);
    return {
      id: text(item.id, `wirePayloads[${index}].id`),
      major: Number(item.major),
      file: text(item.file, `wirePayloads[${index}].file`),
      sha256: text(item.sha256, `wirePayloads[${index}].sha256`),
      consumer,
    };
  });
  const policy = value.wirePolicy;
  const additiveFields = text(
    policy.additiveFields,
    "wirePolicy.additiveFields",
  );
  const unknownCapabilities = text(
    policy.unknownCapabilities,
    "wirePolicy.unknownCapabilities",
  );
  const unknownEnumValues = text(
    policy.unknownEnumValues,
    "wirePolicy.unknownEnumValues",
  );
  if (
    additiveFields !== "tolerated" ||
    unknownCapabilities !== "tolerated" ||
    unknownEnumValues !== "tolerated"
  )
    throw new CompatibilityError(
      "wire policy must tolerate all unknown values",
    );
  return {
    peers,
    cells,
    wirePayloads: wires,
    wirePolicy: { additiveFields, unknownCapabilities, unknownEnumValues },
  };
};
