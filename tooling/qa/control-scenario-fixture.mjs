import { randomUUID } from "node:crypto";
import { mkdir, readFile, readdir, rename, rm } from "node:fs/promises";
import { join } from "node:path";

export const root = join(import.meta.dirname, "../..");
export const serverRoot = join(root, "apps/server");
export const controlFixtureRoot = join(root, "apps/control/test/fixtures");
const fixturePath = process.env.CONTROL_FIXTURE;
export const output = process.env.CONTROL_OUTPUT;
export const qaEnabled = fixturePath !== undefined && output !== undefined;

if (qaEnabled) {
  await mkdir(output, { recursive: true });
  const owned = (await readdir(output, { withFileTypes: true }))
    .filter((entry) => entry.isFile() && (
      entry.name.endsWith(".png") ||
      /^control-policy-(?:happy|failure)\.json$/.test(entry.name)
    ));
  if (owned.length > 0) {
    const quarantine = join(output, `.control-stale-${randomUUID()}`);
    await mkdir(quarantine);
    for (const entry of owned) {
      await rename(join(output, entry.name), join(quarantine, entry.name));
    }
    await rm(quarantine, { recursive: true, force: true });
  }
}
const fixture = qaEnabled ? await readFile(fixturePath, "utf8") : "";
export const scenario = fixture.match(/^scenario:\s*(.+)$/m)?.[1];
if (qaEnabled && scenario !== "control-policy-happy" && scenario !== "control-policy-failure") {
  throw new TypeError("unsupported Control QA scenario");
}
const expectedByScenario = {
  "control-policy-happy": [
    "real-todo13-authenticated-discovery-and-pairing",
    "catalog-browse-and-search",
    "zone-renderer-assignment",
    "two-queue-adds-and-reorder",
    "play-pause-seek-resume-next-stop",
    "desktop-mobile-keyboard-overflow-evidence",
    "no-secret-evidence",
  ],
  "control-policy-failure": [
    "certificate-mismatch-and-rejected-token-recovery",
    "renderer-offline-no-blind-retry",
    "blocked-track-retry-and-skip",
    "stale-revision-preserves-intent-no-blind-retry",
    "command-failure",
    "event-sequence-gap-full-resync",
    "token-revocation-clear-and-repair",
    "desktop-mobile-keyboard-overflow-evidence",
    "no-secret-evidence",
  ],
};
const fixtureExpects = [...fixture.matchAll(/^\s+-\s+(.+)$/gm)].map((match) => match[1]);
if (qaEnabled && JSON.stringify(fixtureExpects) !== JSON.stringify(expectedByScenario[scenario])) {
  throw new TypeError("Control fixture expects list drifted");
}
