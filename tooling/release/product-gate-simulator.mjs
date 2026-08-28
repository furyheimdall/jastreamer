import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { createPromotionFixture } from "./product-gate-fixture.mjs";
import { verifyProductGate } from "./product-gate.mjs";

const sha256 = (value) => createHash("sha256").update(value).digest("hex");
const observed = (command, arguments_, filter) => {
  try {
    return execFileSync(command, arguments_, { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] })
      .split("\n").map((line) => line.trim()).filter((line) => line !== "" && filter(line)).map(sha256).sort();
  } catch {
    return [];
  }
};
const inventory = () => [
  { type: "process", ids: observed("ps", ["-eo", "pid=,comm="], (line) => /jastreamer/i.test(line)) },
  { type: "container", ids: observed("docker", ["ps", "--format", "{{.ID}} {{.Names}}"], (line) => /jastreamer/i.test(line)) },
  { type: "temporary", ids: observed("find", ["/tmp", "-maxdepth", "1", "-type", "d", "-name", "product-gate-owned-*", "-printf", "%f\n"], () => true) },
  { type: "listener", ids: observed("ss", ["-lntupH"], (line) => /jastreamer/i.test(line)) },
  { type: "builder", ids: observed("docker", ["buildx", "ls"], (line) => /jastreamer/i.test(line)) },
  { type: "browser", ids: observed("ps", ["-eo", "pid=,comm="], (line) => /chromium|chrome|firefox/i.test(line)) },
];

const outputIndex = process.argv.indexOf("--output");
if (outputIndex < 0 || process.argv[outputIndex + 1] === undefined) throw new TypeError("PRODUCT_GATE_SIMULATOR_USAGE");
const output = resolve(process.argv[outputIndex + 1]);
const nowIndex = process.argv.indexOf("--now");
const now = nowIndex < 0 ? new Date().toISOString() : process.argv[nowIndex + 1];
mkdirSync(output, { recursive: true });
const before = inventory();
writeFileSync(join(output, "inventory-pre.json"), `${JSON.stringify(before, null, 2)}\n`);

const happyRoot = join(output, "happy-fixture");
const happy = await createPromotionFixture(happyRoot, now, before);
const accepted = verifyProductGate(happy.receiptPath, { root: happyRoot, now, profile: "fixture", trustConfigPath: happy.trustConfigPath, mutationLedgerPath: happy.mutationLedgerPath });
writeFileSync(join(output, "authorized-simulation.json"), `${JSON.stringify(accepted, null, 2)}\n`);

const failureRoot = join(output, "failure-fixture");
const failure = await createPromotionFixture(failureRoot, now, before);
const priorPath = join(failureRoot, "prior-published/server-v0.0.9.bin"); const priorBefore = sha256(readFileSync(priorPath));
const alteredPath = join(failureRoot, failure.receipt.candidates.control.artifacts[0].path);
writeFileSync(alteredPath, "altered exact candidate bytes\n");
const rejected = verifyProductGate(failure.receiptPath, { root: failureRoot, now, profile: "fixture", trustConfigPath: failure.trustConfigPath, mutationLedgerPath: failure.mutationLedgerPath });
writeFileSync(join(output, "altered-digest-denial.json"), `${JSON.stringify(rejected, null, 2)}\n`);
rmSync(join(failureRoot, "stage"), { recursive: true, force: true });
const failureCleanup = { stagingRemoved: true, priorPublishedTouched: sha256(readFileSync(priorPath)) !== priorBefore, priorPublishedSha256: priorBefore, externalMutations: rejected.externalMutations };
writeFileSync(join(output, "failed-staging-cleanup.json"), `${JSON.stringify(failureCleanup, null, 2)}\n`);

const pendingRoot = join(output, "pending-fixture");
const pending = await createPromotionFixture(pendingRoot, now, before);
pending.receipt.qualifications.k17.status = "pending";
pending.resignReceipt();
const pendingDenied = verifyProductGate(pending.receiptPath, { root: pendingRoot, now, profile: "fixture", trustConfigPath: pending.trustConfigPath, mutationLedgerPath: pending.mutationLedgerPath });
writeFileSync(join(output, "pending-physical-denial.json"), `${JSON.stringify(pendingDenied, null, 2)}\n`);

const after = inventory();
writeFileSync(join(output, "inventory-post.json"), `${JSON.stringify(after, null, 2)}\n`);
const ledger = readFileSync(happy.mutationLedgerPath, "utf8") + readFileSync(failure.mutationLedgerPath, "utf8") + readFileSync(pending.mutationLedgerPath, "utf8");
writeFileSync(join(output, "observed-mutation-ledger.jsonl"), ledger);
if (!accepted.ok || rejected.ok || rejected.code !== "DIGEST_MISMATCH" || pendingDenied.ok || pendingDenied.code !== "QUALIFICATION_PENDING" || failureCleanup.priorPublishedTouched || JSON.stringify(before) !== JSON.stringify(after) || ledger.split("\n").filter(Boolean).some((line) => JSON.parse(line).externallyObserved)) process.exit(1);
console.log(JSON.stringify({ accepted: accepted.ok, selected: accepted.selection.length, rendererAssets: accepted.rendererPublicAssets.length, rebuilt: accepted.rebuild, rejected: rejected.code, pendingRejected: pendingDenied.code, externalMutations: 0 }));
