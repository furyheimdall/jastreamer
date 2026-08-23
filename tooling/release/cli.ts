import { writeFileSync } from "node:fs";
import { validateRelease } from "./validator";

const fixtureIndex = process.argv.findIndex((arg) => arg === "--fixtures" || arg === "--fixture");
const directory = fixtureIndex >= 0 ? process.argv[fixtureIndex + 1] : undefined;
const snapshotIndex = process.argv.indexOf("--snapshot");
const snapshot = snapshotIndex >= 0 ? process.argv[snapshotIndex + 1] : undefined;
if (!directory) { console.error("FIXTURES_REQUIRED"); process.exit(65); }
const result = validateRelease(directory);
if (!result.ok) { for (const error of result.errors) console.error(error); process.exit(65); }
if (snapshot) writeFileSync(snapshot, `${JSON.stringify(result.manifests, null, 2)}\n`);
console.log(JSON.stringify(result.manifests));
