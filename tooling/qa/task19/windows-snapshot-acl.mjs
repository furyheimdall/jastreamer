import { spawnSync } from "node:child_process";

const TRUSTED_SIDS = new Set(["S-1-5-18", "S-1-5-32-544"]);
const currentRunnerSidScript = String.raw`[Security.Principal.WindowsIdentity]::GetCurrent().User.Value`;
const setAclScript = String.raw`
param([string]$Path, [string]$RunnerSid)
$current = [Security.Principal.WindowsIdentity]::GetCurrent().User
if ($current.Value -ne $RunnerSid) { throw 'TASK19_RUNNER_SID_DRIFT' }
$runner = [Security.Principal.SecurityIdentifier]::new($RunnerSid)
$acl = [Security.AccessControl.DirectorySecurity]::new()
$acl.SetAccessRuleProtection($true, $false)
$acl.SetOwner($runner)
$inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'
$propagation = [Security.AccessControl.PropagationFlags]::None
$rights = [Security.AccessControl.FileSystemRights]::FullControl
foreach ($sid in @($runner, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'), [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) {
  $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, $rights, $inherit, $propagation, 'Allow'))
}
Set-Acl -LiteralPath $Path -AclObject $acl
`;
const inspectAclScript = String.raw`
param([string]$Path)
$acl = Get-Acl -LiteralPath $Path
[ordered]@{ owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value; protected = $acl.AreAccessRulesProtected; rules = @($acl.Access | ForEach-Object { [ordered]@{ sid = $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value; type = $_.AccessControlType.ToString(); inherited = $_.IsInherited; rights = $_.FileSystemRights.ToString() } }) } | ConvertTo-Json -Depth 5 -Compress
`;
const executePowerShell = (script, ...arguments_) => spawnSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", script, ...arguments_], { encoding: "utf8", windowsHide: true });

export const validateSnapshotAcl = (value, runnerSid) => {
  if (!/^S-1-5-(?:[0-9]+-)+[0-9]+$/.test(runnerSid) || value === null || typeof value !== "object" || Array.isArray(value) || value.protected !== true || value.owner !== runnerSid || !Array.isArray(value.rules)) return { ok: false, code: "TASK19_SNAPSHOT_ACL_INVALID" };
  const allowed = new Set([...TRUSTED_SIDS, runnerSid]);
  const invalid = value.rules.find((rule) => rule === null || typeof rule !== "object" || Array.isArray(rule) || !allowed.has(rule.sid) || rule.type !== "Allow" || rule.inherited !== false || !String(rule.rights).includes("FullControl"));
  const present = new Set(value.rules.map((rule) => rule.sid));
  return invalid === undefined && [...allowed].every((sid) => present.has(sid)) ? { ok: true } : { ok: false, code: "TASK19_SNAPSHOT_ACL_UNTRUSTED" };
};

export const secureWindowsSnapshot = (path, options = {}) => {
  if ((options.platform ?? process.platform) !== "win32") return { ok: true, applied: false };
  const execute = options.execute ?? executePowerShell;
  const identity = options.runnerSid === undefined ? execute(currentRunnerSidScript) : { status: 0, stdout: options.runnerSid };
  const runnerSid = identity.status === 0 && typeof identity.stdout === "string" ? identity.stdout.trim() : "";
  if (!/^S-1-5-(?:[0-9]+-)+[0-9]+$/.test(runnerSid)) throw new Error("TASK19_RUNNER_SID_INVALID");
  const applied = execute(setAclScript, path, runnerSid); if (applied.status !== 0) throw new Error("TASK19_SNAPSHOT_ACL_APPLY_FAILED");
  const inspected = execute(inspectAclScript, path); if (inspected.status !== 0 || typeof inspected.stdout !== "string") throw new Error("TASK19_SNAPSHOT_ACL_VERIFY_FAILED");
  let value; try { value = JSON.parse(inspected.stdout); } catch { throw new Error("TASK19_SNAPSHOT_ACL_VERIFY_FAILED"); }
  const validation = validateSnapshotAcl(value, runnerSid); if (!validation.ok) throw new Error(validation.code);
  return { ok: true, applied: true, runnerSid };
};
