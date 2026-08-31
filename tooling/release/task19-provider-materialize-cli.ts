import { readFileSync, readdirSync } from "node:fs";
import { join, relative, resolve, sep } from "node:path";
import { materializePrivateDirectory, platformPrivateMaterializationAdapter } from "./task19-private-materializer";

class PhysicalMaterializationError extends Error {
  readonly name = "PhysicalMaterializationError";
  constructor(readonly code: string) { super(code); }
}

const fail = (code: string): never => { throw new PhysicalMaterializationError(code); };

const main = (): void => {
  const args = process.argv.slice(2);
  if (args.length !== 6 || args[0] !== "--renderer-input-root" || args[2] !== "--k17-input" || args[4] !== "--output-root") fail("TASK19_PROVIDER_MATERIALIZE_USAGE");
  const rendererRoot = resolve(args[1] ?? fail("TASK19_PROVIDER_MATERIALIZE_USAGE"));
  const k17Input = resolve(args[3] ?? fail("TASK19_PROVIDER_MATERIALIZE_USAGE"));
  const outputRoot = resolve(args[5] ?? fail("TASK19_PROVIDER_MATERIALIZE_USAGE"));
  const adapter = platformPrivateMaterializationAdapter();
  const entries: Record<string, Uint8Array> = {};
  const collect = (root: string, current: string): void => {
    adapter.assertNoReparseComponents(current);
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name);
      adapter.assertNoReparseComponents(path);
      if (entry.isDirectory()) collect(root, path);
      else if (entry.isFile()) {
        const name = relative(root, path).split(sep).join("/");
        if (name === "" || entries[name] !== undefined) fail("TASK19_PROVIDER_INPUT_INVENTORY_INVALID");
        entries[name] = readFileSync(path);
      } else fail("TASK19_PROVIDER_INPUT_REPARSE_REJECTED");
    }
  };
  collect(rendererRoot, rendererRoot);
  adapter.assertNoReparseComponents(k17Input);
  if (entries["k17-qualification.json"] !== undefined) fail("TASK19_PROVIDER_INPUT_INVENTORY_INVALID");
  entries["k17-qualification.json"] = readFileSync(k17Input);
  if (entries["manifest.json"] === undefined || entries["windows-audio-qualification.json"] === undefined || !Object.keys(entries).some((path) => /^jastreamer-renderer_[0-9]+\.[0-9]+\.[0-9]+_windows_amd64\.msi$/.test(path))) fail("TASK19_PROVIDER_INPUT_INVENTORY_INVALID");
  materializePrivateDirectory(outputRoot, entries, adapter);
  process.stdout.write(`${JSON.stringify({ status: "materialized", candidateBytesWritten: Object.values(entries).reduce((total, bytes) => total + bytes.byteLength, 0), externalWrites: 0 })}\n`);
};

try { main(); } catch (error) { // no-excuse-ok: catch
  const code = error instanceof PhysicalMaterializationError ? error.code : error instanceof Error && /^[A-Z0-9_]+$/.test(error.message) ? error.message : "TASK19_PROVIDER_MATERIALIZE_FAILED";
  process.stdout.write(`${JSON.stringify({ status: "pending", code, candidateBytesWritten: 0, externalWrites: 0 })}\n`);
  process.exitCode = 77;
}
