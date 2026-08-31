import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const fixtureRoot = resolve(import.meta.dirname);
export const loadSyntheticTrustForTest = () => JSON.parse(readFileSync(resolve(fixtureRoot, "task19-synthetic-trust-v1.json")));
