#!/usr/bin/env node
import { createHash, createPrivateKey, createPublicKey, sign } from "node:crypto";
import { lstat, readFile, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, relative, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

const fail = (code) => { throw new Error(code); };
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const boundPath = async (root, path) => {
  if (typeof path !== "string" || isAbsolute(path) || path.split(/[\\/]/).includes("..")) fail("TASK19_SIGNER_REFERENCE_INVALID");
  const absolute = resolve(root, path); const inside = relative(root, absolute); if (inside === "" || inside === ".." || inside.startsWith(`..${sep}`) || isAbsolute(inside)) fail("TASK19_SIGNER_REFERENCE_INVALID");
  let current = root; for (const part of inside.split(sep)) { current = resolve(current, part); if ((await lstat(current)).isSymbolicLink()) fail("TASK19_SIGNER_REPARSE_POINT_REJECTED"); }
  return absolute;
};

export const signExecutionEvidence = async ({ executionPath, keyPath, outputPath }) => {
  const executionAbsolute = resolve(executionPath); const execution = JSON.parse(await readFile(executionAbsolute, "utf8"));
  if (execution.evidenceRoot !== ".") fail("TASK19_SIGNER_EVIDENCE_ROOT_INVALID"); const evidenceRoot = dirname(executionAbsolute);
  const privateKey = createPrivateKey(await readFile(resolve(keyPath))); const publicKey = createPublicKey(privateKey); const publicBytes = publicKey.export({ type: "spki", format: "der" }); const keyId = sha256(publicBytes);
  const references = [...(execution.installedProductReceipt?.runs ?? []), execution.installedProductReceipt?.performance, execution.installedProductReceipt?.probes].filter(Boolean);
  for (const reference of references) { const bytes = await readFile(await boundPath(evidenceRoot, reference.path)); reference.keyId = keyId; reference.signature = sign(null, bytes, privateKey).toString("base64"); }
  execution.keyId = keyId; execution.publicKey = publicKey.export({ type: "spki", format: "pem" }).toString();
  await writeFile(resolve(outputPath), `${JSON.stringify(execution, null, 2)}\n`, { mode: 0o400, flag: "wx" });
  return execution;
};

const main = async () => { const values = process.argv.slice(2); if (values.length !== 6 || values[0] !== "--execution" || values[2] !== "--key" || values[4] !== "--output") fail("TASK19_SIGNER_USAGE"); const result = await signExecutionEvidence({ executionPath: values[1], keyPath: values[3], outputPath: values[5] }); process.stdout.write(`${JSON.stringify({ schemaVersion: 1, kind: "task19_signer_result", keyId: result.keyId, signedReferences: result.installedProductReceipt.runs.length + 2 })}\n`); };
if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) await main();
