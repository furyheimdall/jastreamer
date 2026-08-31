#!/usr/bin/env bun
import { mkdirSync, writeFileSync } from "node:fs";
import * as qa from "./qa";
import { cleanup, createDataVolume } from "./runtime";

const root = process.argv[2];
if (root === undefined) throw new Error("SIGNAL_FIXTURE_ROOT_REQUIRED");
const install: unknown = Reflect.get(qa, "installSignalCleanup");
if (typeof install !== "function") throw new Error("SIGNAL_CLEANUP_NOT_INSTALLED");
const volume = `jastreamer-task17-signal-${process.pid}`; const resources = { names: new Set<string>(), projects: new Set<string>(), volumes: new Set<string>() };
install(() => { cleanup(resources, "/unused-compose.yaml"); qa.removeWorkspace(root); });
mkdirSync(root, { recursive: true });
writeFileSync(`${root}/descendant`, "owned");
createDataVolume(volume, resources.volumes);
console.log(`OWNED_RESOURCES_CREATED ${volume}`);
await new Promise<never>(() => {});
