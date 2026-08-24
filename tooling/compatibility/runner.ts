import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { read, parseMatrix, CompatibilityError } from "./parser";
import { runMatrix } from "./executor";

const args = process.argv.slice(2);
const matrixIndex = args.indexOf("--matrix");
const fixtureIndex = args.indexOf("--fixture");
const outputIndex = args.indexOf("--output");
const input =
  matrixIndex >= 0
    ? args[matrixIndex + 1]
    : fixtureIndex >= 0
      ? args[fixtureIndex + 1]
      : undefined;
const output = outputIndex >= 0 ? args[outputIndex + 1] : undefined;
if (!input || !output) {
  console.error(
    "usage: compatibility run <--matrix|--fixture> <path> --output <file>",
  );
  process.exit(64);
}
try {
  const workspaceRoot = resolve(import.meta.dir, "../..");
  const fixtureRoot = dirname(resolve(input));
  const results = runMatrix(
    workspaceRoot,
    fixtureRoot,
    parseMatrix(read(input)),
  );
  const passed = results.filter((result) => result.status === "passed").length;
  const failed = results.length - passed;
  const runnable = (prefix: string): boolean =>
    results
      .filter((result) => result.id.startsWith(prefix))
      .every((result) => result.status === "passed");
  mkdirSync(dirname(resolve(output)), { recursive: true });
  writeFileSync(
    output,
    `${JSON.stringify(
      {
        protocol: { current: 2, previous: 1 },
        summary: {
          passed,
          failed,
          siblingReleaseGates: {
            control: runnable("control-candidate-"),
            renderer: runnable("renderer-candidate-"),
          },
        },
        results,
      },
      null,
      2,
    )}\n`,
  );
  if (results.some((result) => result.status === "failed")) process.exit(65);
} catch (error) {
  console.error(
    error instanceof CompatibilityError
      ? `INVALID_FIXTURE: ${error.message}`
      : "INVALID_FIXTURE: unable to read fixture",
  );
  process.exit(65);
}
