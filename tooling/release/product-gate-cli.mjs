import { writeFileSync } from "node:fs";
import { verifyProductGate } from "./product-gate.mjs";

const names = new Set(["--receipt", "--root", "--trust-config", "--profile", "--mutation-ledger", "--output", "--now"]);
const parse = (arguments_) => {
  const values = new Map();
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index];
    const value = arguments_[index + 1];
    if (!names.has(name) || value === undefined || values.has(name)) throw new TypeError("PRODUCT_GATE_USAGE");
    values.set(name, value);
  }
  for (const required of ["--receipt", "--root", "--trust-config", "--mutation-ledger", "--output"])  if (!values.has(required)) throw new TypeError("PRODUCT_GATE_USAGE");
  return values;
};

try {
  const values = parse(process.argv.slice(2));
  const result = verifyProductGate(values.get("--receipt"), {
    root: values.get("--root"), trustConfigPath: values.get("--trust-config"), profile: values.get("--profile") ?? "production",
    repositoryRoot: process.cwd(), mutationLedgerPath: values.get("--mutation-ledger"), now: values.get("--now") ?? new Date().toISOString(),
  });
  const output = result.ok
    ? { schemaVersion: 1, kind: "product_gate_verification", status: "authorized", ...result }
    : { schemaVersion: 1, kind: "product_gate_verification", status: "denied", ...result };
  writeFileSync(values.get("--output"), `${JSON.stringify(output, null, 2)}\n`);
  if (!result.ok) {
    console.error(result.code);
    process.exit(65);
  }
  console.log(JSON.stringify(output));
} catch (error) {
  // no-excuse-ok: catch -- CLI boundary converts usage and filesystem failures to a typed denial.
  if (error instanceof Error) {
    console.error(error.message);
    process.exit(64);
  }
  throw error;
}
