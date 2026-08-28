import { createHash } from "node:crypto";
import { closeSync, constants, fstatSync, lstatSync, mkdirSync, mkdtempSync, openSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { PublicationContractError } from "./publication-parse";
import type { PreparedPublication } from "./publication-types";

const sha256 = (bytes: NodeJS.ArrayBufferView | string): string => createHash("sha256").update(bytes).digest("hex");

export const resolveInside = (root: string, path: string): string => {
  const absoluteRoot = resolve(root);
  const absolute = isAbsolute(path) ? resolve(path) : resolve(absoluteRoot, path);
  const location = relative(absoluteRoot, absolute);
  if (location === "" || location === ".." || location.startsWith(`..${sep}`) || isAbsolute(location)) throw new PublicationContractError("PUBLICATION_PATH_INVALID");
  return absolute;
};

export const stableRead = (path: string): Buffer => {
  let descriptor: number | undefined;
  try {
    const namedBefore = lstatSync(path, { bigint: true });
    if (namedBefore.isSymbolicLink() || !namedBefore.isFile()) throw new PublicationContractError("PUBLICATION_FILE_INVALID");
    descriptor = openSync(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const before = fstatSync(descriptor, { bigint: true });
    const bytes = readFileSync(descriptor);
    const after = fstatSync(descriptor, { bigint: true });
    const namedAfter = lstatSync(path, { bigint: true });
    if (!before.isFile() || namedAfter.isSymbolicLink() || !namedAfter.isFile()
      || before.dev !== after.dev || before.ino !== after.ino || before.size !== after.size || before.mtimeNs !== after.mtimeNs
      || namedBefore.dev !== before.dev || namedBefore.ino !== before.ino || namedAfter.dev !== before.dev || namedAfter.ino !== before.ino
      || BigInt(bytes.byteLength) !== before.size) throw new PublicationContractError("PUBLICATION_FILE_REPLACED");
    return bytes;
  } catch (error) {
    if (error instanceof PublicationContractError) throw error;
    throw new PublicationContractError("PUBLICATION_FILE_INVALID");
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
  }
};

export const readExact = (path: string, expectedSha256: string): Buffer => {
  const bytes = stableRead(path);
  if (sha256(bytes) !== expectedSha256) throw new PublicationContractError("PUBLICATION_DIGEST_MISMATCH");
  return bytes;
};

const exactStageInventory = (prepared: PreparedPublication): void => {
  const expected = prepared.artifacts.map((artifact) => artifact.name).sort();
  let actual: string[];
  try {
    const entries = readdirSync(prepared.request.stageRoot, { withFileTypes: true });
    if (entries.some((entry) => !entry.isFile() || entry.isSymbolicLink())) throw new PublicationContractError("PUBLICATION_STAGE_INVALID");
    actual = entries.map((entry) => entry.name).sort();
  } catch (error) {
    if (error instanceof PublicationContractError) throw error;
    throw new PublicationContractError("PUBLICATION_STAGE_INVALID");
  }
  if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new PublicationContractError("PUBLICATION_STAGE_INVALID");
};

const closureDigest = (files: readonly { readonly path: string; readonly sha256: string }[]): string => {
  const records = files.map((file) => {
    const bytes = readExact(file.path, file.sha256);
    return { path: file.path, sha256: sha256(bytes), size: bytes.byteLength };
  });
  return sha256(JSON.stringify(records));
};

export const rehashPublicationClosure = (prepared: PreparedPublication): string => {
  exactStageInventory(prepared);
  return closureDigest(prepared.immutableFiles);
};

export const rehashCleanupAuthorization = (prepared: PreparedPublication): string => closureDigest(prepared.authorizationFiles);

const immutableIdentity = (file: Readonly<{ path: string; sha256: string }>): string => JSON.stringify([file.path, file.sha256]);

export const approveProviderTransfer = (prepared: PreparedPublication): { readonly prepared: PreparedPublication; readonly dispose: () => void } => {
  mkdirSync(dirname(prepared.request.receiptPath), { recursive: true, mode: 0o700 });
  const root = mkdtempSync(join(dirname(prepared.request.receiptPath), ".publication-approved-"));
  try {
    const artifacts = prepared.artifacts.map((artifact) => {
      const bytes = readExact(artifact.absolutePath, artifact.sha256);
      const absolutePath = join(root, artifact.name);
      writeFileSync(absolutePath, bytes, { flag: "wx", mode: 0o400 });
      return { ...artifact, absolutePath };
    });
    const selectedIdentities = new Set(prepared.artifacts.map((artifact) => immutableIdentity({ path: artifact.absolutePath, sha256: artifact.sha256 })));
    if (selectedIdentities.size !== prepared.artifacts.length || prepared.authorizationFiles.some((file) => selectedIdentities.has(immutableIdentity(file)))) throw new PublicationContractError("PUBLICATION_CLOSURE_INVALID");
    const foundSelected = new Set<string>();
    const retainedImmutableFiles = prepared.immutableFiles.filter((file) => {
      const identity = immutableIdentity(file);
      if (!selectedIdentities.has(identity)) return true;
      foundSelected.add(identity);
      return false;
    });
    if (foundSelected.size !== selectedIdentities.size || prepared.authorizationFiles.some((required) => !retainedImmutableFiles.some((file) => immutableIdentity(file) === immutableIdentity(required)))) throw new PublicationContractError("PUBLICATION_CLOSURE_INVALID");
    const approved: PreparedPublication = {
      ...prepared,
      request: { ...prepared.request, stageRoot: root },
      artifacts,
      immutableFiles: [...retainedImmutableFiles, ...artifacts.map((artifact) => ({ path: artifact.absolutePath, sha256: artifact.sha256 }))],
    };
    rehashPublicationClosure(approved);
    return { prepared: approved, dispose: () => rmSync(root, { recursive: true, force: true }) };
  } catch (error) {
    rmSync(root, { recursive: true, force: true });
    throw error;
  }
};

export const fileSha256 = (path: string): string => sha256(stableRead(path));
