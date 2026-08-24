import { readFileSync } from "node:fs";
import { resolve, sep } from "node:path";
import {
  CompatibilityError,
  isRecord,
  read,
  sha,
  stringList,
  text,
} from "./parser";
import type { WireRef } from "./parser";
export const decodeWire = (
  root: string,
  ref: WireRef,
): {
  readonly major: number;
  readonly capabilities: readonly string[];
  readonly assertions: readonly string[];
} => {
  const base = resolve(root);
  const path = resolve(base, ref.file);
  if (!path.startsWith(`${base}${sep}`))
    throw new CompatibilityError(`wire path escapes fixture root for ${ref.id}`);
  const bytes = readFileSync(path);
  if (sha(bytes) !== ref.sha256)
    throw new CompatibilityError(`wire digest mismatch for ${ref.id}`);
  const value = read(path);
  if (!isRecord(value)) throw new CompatibilityError(`${ref.id} wire invalid`);
  const major = Number(value.protocolMajor);
  if (!Number.isInteger(major) || major !== ref.major)
    throw new CompatibilityError(`${ref.id} protocol major mismatch`);
  const capabilities = stringList(value.capabilities, `${ref.id}.capabilities`);
  if (
    text(value.optionalMetadata, `${ref.id}.optionalMetadata`) !== "additive" ||
    !capabilities.includes("future-capability") ||
    text(value.commandKind, `${ref.id}.commandKind`) !== "future-command"
  )
    throw new CompatibilityError(`${ref.id} unknown-value oracle failed`);
  if (ref.consumer === "control") {
    if (
      text(value.requestId, `${ref.id}.requestId`).length === 0 ||
      text(value.continuationPolicy, `${ref.id}.continuationPolicy`) !==
        "future-policy"
    )
      throw new CompatibilityError(`${ref.id} Control required field rejected`);
  } else if (
    text(value.commandId, `${ref.id}.commandId`).length === 0 ||
    typeof value.positionMs !== "number" ||
    !Number.isInteger(value.positionMs) ||
    value.positionMs < 0
  ) {
    throw new CompatibilityError(`${ref.id} Renderer required field rejected`);
  }
  return {
    major,
    capabilities,
    assertions: [
      "required-fields-decoded",
      "additive-field-ignored",
      "unknown-capability-ignored",
      "unknown-enum-typed-rejected",
    ],
  };
};
