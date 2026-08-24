export type Options = Readonly<{ component: "server"; tag: string; version: string; output: string; noPublish: true; failure?: "native-linux-arm64-smoke" }>;
export class UsageError extends Error { readonly exitCode = 64; }
export class GateError extends Error { constructor(message: string, readonly exitCode = 65) { super(message); } }

export function parseArgs(args: readonly string[]): Options {
  const values = new Map<string, string>(); let noPublish = false;
  for (let index = 0; index < args.length; index++) {
    const name = args[index];
    if (name === "--no-publish") { if (noPublish) throw new UsageError("duplicate --no-publish"); noPublish = true; continue; }
    if (!["--component", "--tag", "--output", "--inject-failure"].includes(name ?? "")) throw new UsageError(`unknown argument ${name ?? "end of input"}`);
    const value = args[++index]; if (!value || value.startsWith("--") || values.has(name!)) throw new UsageError(`invalid ${name}`);
    values.set(name!, value);
  }
  if (values.get("--component") !== "server") throw new UsageError("Server release tooling accepts only --component server");
  const tag = values.get("--tag") ?? ""; const match = /^server-v(\d+\.\d+\.\d+)$/.exec(tag);
  if (!match) throw new GateError("PROTECTED_SERVER_TAG_REQUIRED");
  if (!noPublish) throw new UsageError("local release rehearsal requires --no-publish");
  const output = values.get("--output"); if (!output) throw new UsageError("missing --output");
  const failure = values.get("--inject-failure");
  if (failure && failure !== "native-linux-arm64-smoke") throw new UsageError("unsupported failure injection");
  return { component: "server", tag, version: match[1]!, output, noPublish: true, failure: failure as Options["failure"] };
}
