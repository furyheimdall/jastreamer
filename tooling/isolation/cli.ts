import { atomicWriteJson } from "./io.ts";
import { verifyIsolation } from "./isolation.ts";
import { InputError, parseArguments, parseFixture } from "./parse.ts";
import type { IsolationInput } from "./types.ts";

type Invocation = { readonly input: IsolationInput; readonly output?: string };

const parseInvocation = (args: readonly string[]): Invocation => {
  const fixtureIndex = args.indexOf("--fixture");
  if (fixtureIndex < 0) {
    const outputIndex = args.indexOf("--output");
    const output = outputIndex < 0 ? undefined : args[outputIndex + 1];
    const input = parseArguments(args);
    return output === undefined ? { input } : { input, output };
  }
  const allowed = new Set(["--fixture", "--sparse", "--trace-files", "--output"]);
  const values = new Map<string, string>();
  const switches = new Set<string>();
  for (let index = 0; index < args.length; index += 1) {
    const flag = args[index];
    if (flag === undefined || !allowed.has(flag) || switches.has(flag) || values.has(flag)) throw new InputError("invalid or duplicate fixture option");
    if (flag === "--sparse" || flag === "--trace-files") { switches.add(flag); continue; }
    const value = args[index + 1];
    if (value === undefined || value.startsWith("--")) throw new InputError(`missing value: ${flag}`);
    values.set(flag, value);
    index += 1;
  }
  if (!switches.has("--sparse") || !switches.has("--trace-files")) throw new InputError("--sparse and --trace-files are required");
  const fixture = values.get("--fixture");
  if (fixture === undefined) throw new InputError("--fixture is required");
  const input = parseFixture(fixture);
  const output = values.get("--output");
  return output === undefined ? { input } : { input, output };
};

const main = (): void => {
  try {
    const invocation = parseInvocation(Bun.argv.slice(2));
    const result = verifyIsolation(invocation.input);
    if (invocation.output === undefined) console.log(JSON.stringify(result, null, 2));
    else atomicWriteJson(invocation.output, result);
    if (!result.ok) {
      console.error(JSON.stringify({
        infrastructureFailure: result.infrastructureFailure,
        runDirectoryCleanup: result.runDirectoryCleanup,
        components: result.components.map((component) => ({
          name: component.name,
          status: component.status,
          violations: component.violations,
          error: component.error,
          commandExitCodes: component.commands.map((command) => command.exitCode),
          artifacts: component.package.artifacts,
          cleanup: component.cleanup,
        })),
      }));
    }
    process.exit(result.infrastructureFailure ? 70 : result.ok ? 0 : 65);
  } catch (error) {
    if (error instanceof InputError) {
      console.error(error.message);
      process.exit(64);
    }
    if (error instanceof Error) console.error(error.message);
    else console.error("unknown isolation failure");
    process.exit(70);
  }
};

main();
