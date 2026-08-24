export type Options = Readonly<{
  component: "renderer";
  tag: string;
  version: string;
  output: string;
  noPublish: true;
  scenario: "clean-windows-vm";
  fixture?: string;
}>;

export class UsageError extends Error {
  readonly exitCode = 64;
}

export class GateError extends Error {
  readonly exitCode = 65;
}

export class HostError extends Error {
  readonly exitCode = 69;
}

export class ProtocolError extends Error {
  readonly exitCode = 78;
}

const valueArguments = new Set([
  "--component",
  "--tag",
  "--output",
  "--scenario",
  "--fixture",
]);

export function parseArgs(args: readonly string[]): Options {
  const values = new Map<string, string>();
  let noPublish = false;

  for (let index = 0; index < args.length; index += 1) {
    const name = args[index];
    if (name === undefined) {
      throw new UsageError("unexpected end of input");
    }
    if (name === "--no-publish") {
      if (noPublish) {
        throw new UsageError("duplicate --no-publish");
      }
      noPublish = true;
      continue;
    }
    if (!valueArguments.has(name)) {
      throw new UsageError(`unknown argument ${name}`);
    }
    const value = args[index + 1];
    if (value === undefined || value.startsWith("--") || values.has(name)) {
      throw new UsageError(`invalid ${name}`);
    }
    values.set(name, value);
    index += 1;
  }

  if (values.get("--component") !== "renderer") {
    throw new UsageError("Renderer release tooling accepts only --component renderer");
  }
  const tag = values.get("--tag") ?? "";
  const match = /^renderer-v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(tag);
  if (match === null) {
    throw new GateError("PROTECTED_RENDERER_TAG_REQUIRED");
  }
  if (!noPublish) {
    throw new UsageError("local release rehearsal requires --no-publish");
  }
  if (values.get("--scenario") !== "clean-windows-vm") {
    throw new UsageError("unsupported scenario");
  }
  const output = values.get("--output");
  if (output === undefined) {
    throw new UsageError("missing --output");
  }

  const options = {
    component: "renderer",
    tag,
    version: `${match[1]}.${match[2]}.${match[3]}`,
    output,
    noPublish: true,
    scenario: "clean-windows-vm",
  } as const;
  const fixture = values.get("--fixture");
  return fixture === undefined ? options : { ...options, fixture };
}
