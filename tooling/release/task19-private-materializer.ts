import { createHash } from "node:crypto";
import { chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { spawnSync } from "node:child_process";

export interface PrivateMaterializationAdapter {
  readonly destinationExists: (path: string) => boolean;
  readonly assertNoReparseComponents: (path: string) => void;
  readonly createPrivateSibling: (parent: string) => string;
  readonly queryRunnerSid: () => string;
  readonly applyPrivateAcl: (path: string, runnerSid: string) => void;
  readonly verifyPrivateAcl: (path: string, runnerSid: string) => void;
  readonly createDirectory: (path: string) => void;
  readonly createFileExclusive: (path: string, bytes: Uint8Array) => void;
  readonly readFile: (path: string) => Uint8Array;
  readonly listFiles: (root: string) => readonly string[];
  readonly renameAbsent: (source: string, destination: string) => void;
  readonly removeTree: (path: string) => void;
}

const digest = (bytes: Uint8Array): string => createHash("sha256").update(bytes).digest("hex");
const fail = (code: string): never => { throw new Error(code); };
const stringValue = (value: unknown, code: string): string => typeof value === "string" ? value : fail(code);

export const materializePrivateDirectory = (destination: string, entries: Readonly<Record<string, Uint8Array>>, adapter: PrivateMaterializationAdapter): void => {
  if (adapter.destinationExists(destination)) fail("TASK19_PROVIDER_OUTPUT_EXISTS");
  adapter.assertNoReparseComponents(dirname(destination));
  const staging = adapter.createPrivateSibling(dirname(destination)); let renamed = false;
  try {
    const runnerSid = adapter.queryRunnerSid();
    if (!/^S-1-5-(?:[0-9]+-)+[0-9]+$/.test(runnerSid)) fail("TASK19_RUNNER_SID_INVALID");
    adapter.applyPrivateAcl(staging, runnerSid); adapter.verifyPrivateAcl(staging, runnerSid);
    const inventory = Object.entries(entries).map(([path, bytes]) => {
      if (path.startsWith("/") || path.includes("\\") || path.split("/").some((part) => part === "" || part === "." || part === "..")) fail("TASK19_PROVIDER_PATH_INVALID");
      const target = join(staging, ...path.split("/")); adapter.createDirectory(dirname(target)); adapter.assertNoReparseComponents(dirname(target));
      adapter.createFileExclusive(target, bytes); const written = adapter.readFile(target);
      if (written.byteLength !== bytes.byteLength || digest(written) !== digest(bytes)) fail("TASK19_PROVIDER_STAGING_MISMATCH");
      return { path, size: bytes.byteLength, sha256: digest(bytes) };
    });
    const expectedPaths = inventory.map(({ path }) => path).sort();
    if (inventory.length !== Object.keys(entries).length || JSON.stringify([...adapter.listFiles(staging)].sort()) !== JSON.stringify(expectedPaths)) fail("TASK19_PROVIDER_INVENTORY_MISMATCH");
    if (adapter.destinationExists(destination)) fail("TASK19_PROVIDER_OUTPUT_EXISTS"); adapter.renameAbsent(staging, destination); renamed = true;
    if (JSON.stringify([...adapter.listFiles(destination)].sort()) !== JSON.stringify(expectedPaths)) fail("TASK19_PROVIDER_FINAL_MISMATCH");
    for (const expected of inventory) { const finalPath = join(destination, ...expected.path.split("/")); adapter.assertNoReparseComponents(finalPath); const finalBytes = adapter.readFile(finalPath); if (finalBytes.byteLength !== expected.size || digest(finalBytes) !== expected.sha256) fail("TASK19_PROVIDER_FINAL_MISMATCH"); }
  } catch (error) { adapter.removeTree(renamed ? destination : staging); throw error; }
};

const powershell = (script: string, ...args: readonly string[]) => spawnSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", script, ...args], { encoding: "utf8", windowsHide: true });
const applyAclScript = String.raw`param([string]$Path,[string]$RunnerSid) $current=[Security.Principal.WindowsIdentity]::GetCurrent().User; if($current.Value -ne $RunnerSid){exit 77}; $runner=[Security.Principal.SecurityIdentifier]::new($RunnerSid); $acl=[Security.AccessControl.DirectorySecurity]::new(); $acl.SetAccessRuleProtection($true,$false); $acl.SetOwner($runner); $inherit=[Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'; foreach($sid in @($runner,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'),[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl',$inherit,'None','Allow'))}; Set-Acl -LiteralPath $Path -AclObject $acl`;
const inspectAclScript = String.raw`param([string]$Path) $acl=Get-Acl -LiteralPath $Path; [ordered]@{owner=$acl.GetOwner([Security.Principal.SecurityIdentifier]).Value;protected=$acl.AreAccessRulesProtected;rules=@($acl.Access|ForEach-Object{[ordered]@{sid=$_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value;type=$_.AccessControlType.ToString();inherited=$_.IsInherited;rights=$_.FileSystemRights.ToString()}})}|ConvertTo-Json -Depth 5 -Compress`;
const verifyAcl = (path: string, runnerSid: string): void => { const result = powershell(inspectAclScript, path); if (result.status !== 0) fail("TASK19_PROVIDER_ACL_VERIFY_FAILED"); let parsed: unknown; try { parsed = JSON.parse(result.stdout); } catch { return fail("TASK19_PROVIDER_ACL_VERIFY_FAILED"); } const value = typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? Object.fromEntries(Object.entries(parsed)) : fail("TASK19_PROVIDER_ACL_VERIFY_FAILED"); const rules = Array.isArray(value["rules"]) ? value["rules"] : fail("TASK19_PROVIDER_ACL_VERIFY_FAILED"); const allowed = new Set([runnerSid, "S-1-5-18", "S-1-5-32-544"]); const present = new Set<string>(); for (const raw of rules) { const rule = typeof raw === "object" && raw !== null && !Array.isArray(raw) ? Object.fromEntries(Object.entries(raw)) : fail("TASK19_PROVIDER_ACL_VERIFY_FAILED"); const sid = stringValue(rule["sid"], "TASK19_PROVIDER_ACL_UNTRUSTED"); if (!allowed.has(sid) || rule["type"] !== "Allow" || rule["inherited"] !== false || !String(rule["rights"]).includes("FullControl")) fail("TASK19_PROVIDER_ACL_UNTRUSTED"); present.add(sid); } if (value["owner"] !== runnerSid || value["protected"] !== true || [...allowed].some((sid) => !present.has(sid))) fail("TASK19_PROVIDER_ACL_UNTRUSTED"); };
const assertNoReparseComponents = (path: string): void => {
  const absolute = resolve(path); const parsedRoot = absolute.startsWith(sep) ? sep : `${absolute.slice(0, 2)}${sep}`; let current = parsedRoot;
  for (const part of absolute.slice(parsedRoot.length).split(/[\\/]/).filter((value) => value !== "")) { current = join(current, part); if (existsSync(current) && lstatSync(current).isSymbolicLink()) fail("TASK19_PROVIDER_REPARSE_POINT_REJECTED"); }
  if (process.platform === "win32") { const result = powershell("param([string]$Path) $p=[IO.Path]::GetFullPath($Path); while($p){ if((Get-Item -LiteralPath $p -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){exit 77}; $next=[IO.Directory]::GetParent($p); if($null -eq $next){break}; $p=$next.FullName }", absolute); if (result.status !== 0) fail("TASK19_PROVIDER_REPARSE_POINT_REJECTED"); }
};
const listFiles = (root: string): readonly string[] => {
  const files: string[] = [];
  const visit = (current: string): void => { assertNoReparseComponents(current); for (const entry of readdirSync(current, { withFileTypes: true })) { const path = join(current, entry.name); assertNoReparseComponents(path); if (entry.isDirectory()) visit(path); else if (entry.isFile()) files.push(relative(root, path).split(sep).join("/")); else fail("TASK19_PROVIDER_REPARSE_POINT_REJECTED"); } };
  visit(root); return files;
};

export const windowsPrivateMaterializationAdapter = (): PrivateMaterializationAdapter => ({
  destinationExists: existsSync,
  assertNoReparseComponents,
  createPrivateSibling: (parent) => mkdtempSync(join(parent, ".task19-provider-")),
  queryRunnerSid: () => { const result = powershell("[Security.Principal.WindowsIdentity]::GetCurrent().User.Value"); return result.status === 0 ? result.stdout.trim() : fail("TASK19_RUNNER_SID_INVALID"); },
  applyPrivateAcl: (path, runnerSid) => { const result = powershell(applyAclScript, path, runnerSid); if (result.status !== 0) fail("TASK19_PROVIDER_ACL_APPLY_FAILED"); },
  verifyPrivateAcl: verifyAcl,
  createDirectory: (path) => { mkdirSync(path, { recursive: true }); },
  createFileExclusive: (path, bytes) => { writeFileSync(path, bytes, { flag: "wx", mode: 0o400 }); },
  readFile: (path) => readFileSync(path),
  listFiles,
  renameAbsent: (source, destination) => { if (existsSync(destination)) fail("TASK19_PROVIDER_OUTPUT_EXISTS"); renameSync(source, destination); },
  removeTree: (path) => { rmSync(path, { recursive: true, force: true }); },
});

export const platformPrivateMaterializationAdapter = (): PrivateMaterializationAdapter => process.platform === "win32" ? windowsPrivateMaterializationAdapter() : {
  destinationExists: existsSync,
  assertNoReparseComponents,
  createPrivateSibling: (parent) => mkdtempSync(join(parent, ".task19-provider-")),
  queryRunnerSid: () => "S-1-5-21-1",
  applyPrivateAcl: (path) => { chmodSync(path, 0o700); },
  verifyPrivateAcl: (path) => { if ((lstatSync(path).mode & 0o077) !== 0) fail("TASK19_PROVIDER_ACL_INVALID"); },
  createDirectory: (path) => { mkdirSync(path, { recursive: true, mode: 0o700 }); },
  createFileExclusive: (path, bytes) => { writeFileSync(path, bytes, { flag: "wx", mode: 0o400 }); },
  readFile: (path) => readFileSync(path),
  listFiles,
  renameAbsent: (source, destination) => { if (existsSync(destination)) fail("TASK19_PROVIDER_OUTPUT_EXISTS"); renameSync(source, destination); },
  removeTree: (path) => { rmSync(path, { recursive: true, force: true }); },
};
