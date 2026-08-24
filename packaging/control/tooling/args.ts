export type Options = Readonly<{
  component: "control";
  tag: string;
  version: string;
  output: string;
  noPublish: true;
  scenario: "android-in-place-upgrade";
  fixture?: string;
}>;

export class UsageError extends Error { readonly exitCode = 64; }
export class GateError extends Error { constructor(message: string, readonly exitCode = 65) { super(message); } }

export function parseArgs(args: readonly string[]): Options {
  const values = new Map<string, string>();
  let noPublish = false;
  for (let index = 0; index < args.length; index++) {
    const name = args[index];
    if (name === undefined) throw new UsageError("unexpected end of input");
    if (name === "--no-publish") {
      if (noPublish) throw new UsageError("duplicate --no-publish");
      noPublish = true;
      continue;
    }
    if (!["--component", "--tag", "--output", "--scenario", "--fixture"].includes(name)) {
      throw new UsageError(`unknown argument ${name}`);
    }
    const value = args[++index];
    if (!value || value.startsWith("--") || values.has(name)) throw new UsageError(`invalid ${name}`);
    values.set(name, value);
  }
  if (values.get("--component") !== "control") throw new UsageError("Control release tooling accepts only --component control");
  const tag = values.get("--tag") ?? "";
  const match = /^control-v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(tag);
  if (!match) throw new GateError("PROTECTED_CONTROL_TAG_REQUIRED");
  if (!noPublish) throw new UsageError("local release rehearsal requires --no-publish");
  if (values.get("--scenario") !== "android-in-place-upgrade") throw new UsageError("unsupported scenario");
  const output = values.get("--output");
  if (!output) throw new UsageError("missing --output");
  return {
    component: "control",
    tag,
    version: `${match[1]}.${match[2]}.${match[3]}`,
    output,
    noPublish: true,
    scenario: "android-in-place-upgrade",
    ...(values.has("--fixture") ? { fixture: values.get("--fixture") } : {}),
  };
}
