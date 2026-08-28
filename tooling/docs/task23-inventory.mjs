import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, readlinkSync } from "node:fs";

const sha = (value) => createHash("sha256").update(value).digest("hex");
const safeRead = (path) => { try { return readFileSync(path); } catch { return undefined; } };
const processRecord = (pid) => {
  const command = safeRead(`/proc/${pid}/cmdline`); const stat = safeRead(`/proc/${pid}/stat`);
  if (command === undefined || stat === undefined) return undefined;
  const text = command.toString().replaceAll("\0", " ").trim();
  const fields = stat.toString().split(" "); const ppid = Number(fields[3]); const started = fields[21] ?? "";
  return { type: "process", id: `pid-${pid}`, pid, ppid, identitySha256: sha(`${pid}\0${started}\0${text}`), commandSha256: sha(text), observedBy: "procfs" };
};
const processTable = () => {
  const records = new Map();
  for (const name of readdirSync("/proc")) if (/^[0-9]+$/.test(name)) { const record = processRecord(Number(name)); if (record) records.set(record.pid, record); }
  return records;
};
const descendants = (records, rootPid) => {
  const ids = new Set([rootPid]); let changed = true;
  while (changed) { changed = false; for (const item of records.values()) if (ids.has(item.ppid) && !ids.has(item.pid)) { ids.add(item.pid); changed = true; } }
  return [...ids].map((pid) => records.get(pid)).filter(Boolean);
};
const orphanRecord = (records) => {
  const value = records.get(1874845); if (!value) return [];
  try {
    const cwd = readlinkSync("/proc/1874845/cwd");
    return cwd.endsWith("/upnp-control/apps/server") ? [value] : [];
  } catch { return []; }
};
const containers = () => {
  try {
    const output = execFileSync("docker", ["ps", "--no-trunc", "--format", "{{json .}}"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
    return output.trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)).filter((item) => /task23|jastreamer/i.test(`${item.Names ?? ""} ${item.Labels ?? ""}`)).map((item) => ({ type: "container", id: `container-${sha(item.ID).slice(0, 16)}`, identitySha256: sha(item.ID), observedBy: "docker-ps" }));
  } catch { return []; }
};
const temporaryDirectories = () => readdirSync("/tmp", { withFileTypes: true }).filter((entry) => entry.isDirectory() && entry.name.startsWith("jastreamer-task23-")).map((entry) => ({ type: "temporary_directory", id: `temp-${sha(entry.name).slice(0, 16)}`, identitySha256: sha(entry.name), observedBy: "procfs-tmpfs" }));
const listeners = (pids) => {
  try {
    const output = execFileSync("ss", ["-H", "-ltnp"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
    const result = [];
    for (const line of output.split("\n")) {
      const owner = line.match(/pid=(\d+)/); const address = line.match(/:(\d+)\s+[^ ]/);
      if (owner && address && pids.has(Number(owner[1]))) { const pid = Number(owner[1]); const port = Number(address[1]); result.push({ type: "listener", id: `listener-${pid}-${port}`, pid, port, identitySha256: sha(`${pid}:${port}`), observedBy: "ss" }); }
    }
    return result;
  } catch { return []; }
};

export const observeResources = (rootPid) => {
  const table = processTable();
  const processes = rootPid === undefined ? orphanRecord(table) : descendants(table, rootPid);
  const pidSet = new Set(processes.map((item) => item.pid));
  const builders = processes.filter((item) => {
    const command = safeRead(`/proc/${item.pid}/cmdline`)?.toString() ?? "";
    return /(?:^|\0)(?:go|cargo|rustc)(?:\0|$)/.test(command);
  }).map((item) => ({ ...item, type: "builder", id: `builder-${item.pid}` }));
  const browsers = processes.filter((item) => {
    const command = safeRead(`/proc/${item.pid}/cmdline`)?.toString() ?? "";
    return /(?:chrome|chromium|firefox)/i.test(command);
  }).map((item) => ({ ...item, type: "browser", id: `browser-${item.pid}` }));
  return { schemaVersion: 1, observedAt: new Date().toISOString(), processes, containers: containers(), listeners: listeners(pidSet), temporaryDirectories: temporaryDirectories(), builders, browsers };
};

export const inventoryResources = (inventory) => [
  ...inventory.processes, ...inventory.containers, ...inventory.listeners,
  ...inventory.temporaryDirectories, ...inventory.builders, ...inventory.browsers,
];
export const inventoryIdentity = (item) => `${item.type}:${item.id}:${item.pid ?? ""}:${item.port ?? ""}:${item.identitySha256}`;

const externalPatterns = [
  /\bgit\s+(?:push|tag)\b/, /\bgh\s+release\s+(?:create|upload|edit|delete)\b/,
  /\bdocker\s+(?:push|login)\b/, /\bskopeo\s+(?:copy|delete)\b/,
];
export const ledgerEntry = ({ sequence, command, previousSha256, sourceText }) => {
  const attemptedOperations = externalPatterns.filter((pattern) => pattern.test(`${command.argv.join(" ")}\n${sourceText}`)).map(String);
  const unsigned = { sequence, commandId: command.id, argvSha256: sha(JSON.stringify(command.argv)), allowlistId: "task23-local-v1", attemptedOperations, externalWriteAttempt: attemptedOperations.length !== 0, previousSha256 };
  return { ...unsigned, entrySha256: sha(JSON.stringify(unsigned)) };
};
