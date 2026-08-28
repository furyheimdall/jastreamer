#!/usr/bin/env bun
import { readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export const PHYSICAL_SCENARIOS = [
  "play-pause-resume-seek-stop", "endpoint-absent", "endpoint-busy",
  "endpoint-invalidated-restored", "duplicate-conflict", "revocation",
  "server-restart", "renderer-restart", "disconnect-before-ack",
  "disconnect-after-ack", "disconnect-before-result", "disconnect-after-result",
  "corrupted-media", "truncated-media", "cleanup",
];

export const runScenarioMatrix = async (runtime) => {
  const scenarios = [];
  try {
    await runtime.launchServerPeer();
    await runtime.launchRenderer();
    await runtime.launchProbe();
    for (const id of PHYSICAL_SCENARIOS) {
      const subscription = runtime.subscribe(id, AbortSignal.timeout(30_000));
      try {
        await runtime.mutate(id);
        const observation = await subscription.signal;
        if (observation.scenario !== id || observation.result !== "passed") throw new Error(`SCENARIO_FAILED:${id}`);
        scenarios.push({ id, result: "passed" });
      } finally {
        subscription.unsubscribe();
      }
    }
    return { scenarios };
  } finally {
    await runtime.cleanup();
  }
};

const parseArgs = (values) => {
  const result = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const key = values[index]; const value = values[index + 1];
    if (key === undefined || value === undefined || !key.startsWith("--")) throw new Error("ARGUMENT_INVALID");
    result.set(key.slice(2), value);
  }
  return result;
};

const invoked = process.argv[1] === undefined ? undefined : pathToFileURL(process.argv[1]).href;
if (invoked === import.meta.url) {
  const args = parseArgs(process.argv.slice(2));
  const configPath = args.get("config");
  const output = args.get("output");
  if (configPath === undefined || output === undefined) throw new Error("ARGUMENT_REQUIRED");
  const config = JSON.parse(await readFile(configPath, "utf8"));
  const { createNativeRuntime } = await import("./windows-audio-native-runtime.mjs");
  const runtime = await createNativeRuntime(config);
  const matrix = await runScenarioMatrix(runtime);
  const result = await runtime.buildQualification(matrix.scenarios);
  await writeFile(output, `${JSON.stringify(result, null, 2)}\n`, { flag: "wx" });
}
