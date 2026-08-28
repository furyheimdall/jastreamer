import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

export const verifyCanonicalSource = (context, canonical, tools) => {
  const sourceRoot = resolve(context.root, canonical.sourceRoot);
  const policyRead = tools.stableRead(context.policyRoot, canonical.sourcePolicyPath);
  if (policyRead.issue) return policyRead.issue;
  const policy = JSON.parse(policyRead.bytes);
  if (policy.schemaVersion !== 1 || typeof policy.policyId !== "string" || !Array.isArray(policy.includeRoots) || !Array.isArray(policy.includeRootFiles) || !Array.isArray(policy.excludePrefixes)) return tools.denied("SOURCE_POLICY_INVALID", canonical.sourcePolicyPath);
  const values = execFileSync("git", ["-C", sourceRoot, "ls-files", "--cached", "--others", "--exclude-standard", "-z"]).toString().split("\0").filter(Boolean).sort();
  const included = [];
  const omitted = [];
  for (const path of values) {
    if (policy.excludePrefixes.some((prefix) => path.startsWith(prefix))) continue;
    const allowed = policy.includeRootFiles.includes(path) || policy.includeRoots.some((root) => path === root || path.startsWith(`${root}/`));
    if (!allowed) omitted.push(path); else included.push(path);
  }
  if (omitted.length !== 0) return tools.denied("SOURCE_POLICY_OMISSION", omitted[0]);
  const records = [];
  for (const path of included) { const read = tools.stableRead(sourceRoot, path); if (read.issue) return read.issue; records.push({ path, sha256: tools.sha256(read.bytes) }); }
  const revision = execFileSync("git", ["-C", sourceRoot, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  return { revision, digest: tools.sha256(JSON.stringify(records)), files: records.map((item) => item.path), policyId: policy.policyId };
};
