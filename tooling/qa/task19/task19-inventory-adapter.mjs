import { spawn } from "node:child_process";

const WINDOWS_INVENTORY = String.raw`
$items = [Collections.Generic.List[object]]::new()
Get-CimInstance Win32_Process | Where-Object { $_.Name -match 'jastreamer|msedge|chrome|adb|emulator' } | ForEach-Object { $items.Add([ordered]@{ type='process'; id="pid-$($_.ProcessId)"; pid=[int]$_.ProcessId; observedBy='windows-cim-process' }) }
Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | ForEach-Object { $items.Add([ordered]@{ type='port'; id="$($_.LocalAddress):$($_.LocalPort)"; observedBy='windows-nettcp-listener' }) }
Get-Process msedge,chrome,chromium -ErrorAction SilentlyContinue | ForEach-Object { $items.Add([ordered]@{ type='browser'; id="pid-$($_.Id)"; pid=[int]$_.Id; observedBy='windows-browser-process' }) }
Get-ChildItem $env:TEMP -Directory -Filter 'task19-*' -ErrorAction SilentlyContinue | ForEach-Object { $items.Add([ordered]@{ type='temporary_directory'; id=$_.FullName; observedBy='windows-temporary-directory' }) }
if (Get-Command docker.exe -ErrorAction SilentlyContinue) { & docker.exe ps --format '{{.ID}}' | ForEach-Object { if ($_) { $items.Add([ordered]@{ type='container'; id=$_; observedBy='docker-process-inventory' }) } } }
if (Get-Command adb.exe -ErrorAction SilentlyContinue) { & adb.exe devices | Select-Object -Skip 1 | ForEach-Object { $serial = ($_ -split '\s+')[0]; if ($serial) { $items.Add([ordered]@{ type='emulator'; id=$serial; observedBy='android-device-inventory' }) } } }
@($items) | ConvertTo-Json -Depth 4 -Compress
`;
const environment = () => Object.fromEntries(["PATH", "Path", "SystemRoot", "WINDIR", "TEMP", "TMP", "ComSpec", "PATHEXT"].flatMap((key) => process.env[key] === undefined ? [] : [[key, process.env[key]]]));
const powershellInventory = () => new Promise((resolve, reject) => {
  const child = spawn("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", WINDOWS_INVENTORY], { env: environment(), stdio: ["ignore", "pipe", "pipe"], windowsHide: true });
  const stdout = []; const stderr = [];
  child.stdout.on("data", (bytes) => stdout.push(bytes)); child.stderr.on("data", (bytes) => stderr.push(bytes)); child.once("error", reject);
  child.once("exit", (code) => { if (code !== 0) reject(new Error(`TASK19_INVENTORY_EXIT:${code}:${Buffer.concat(stderr).toString("utf8").slice(0, 80)}`)); else { try { const value = JSON.parse(Buffer.concat(stdout).toString("utf8") || "[]"); resolve(Array.isArray(value) ? value : [value]); } catch { reject(new Error("TASK19_INVENTORY_OUTPUT_INVALID")); } } });
});

export const createInventoryAdapter = ({ inventory = powershellInventory, owned = () => [], now = () => new Date().toISOString() } = {}) => ({
  capture: async ({ runId, phase }) => {
    if (!/^task19-(?:web|windows|android)-(?:server|control)-first$/.test(runId) || !["before", "allocated", "after"].includes(phase)) throw new Error("TASK19_INVENTORY_REQUEST_INVALID");
    const observed = await inventory({ runId, phase }); const byProcess = new Map(observed.filter((item) => item.type === "process" && Number.isInteger(item.pid)).map((item) => [item.pid, item]));
    const resources = observed.concat(owned().filter((item) => !byProcess.has(item.pid))).map((item) => byProcess.has(item.pid) && item.type === "process" ? { ...item, owner: owned().find((ownedItem) => ownedItem.pid === item.pid)?.owner ?? item.owner } : item);
    if (!Array.isArray(resources) || resources.some((item) => item === null || typeof item !== "object" || typeof item.type !== "string" || typeof item.id !== "string" || typeof item.observedBy !== "string")) throw new Error("TASK19_INVENTORY_OUTPUT_INVALID");
    return { capturedAt: now(), resources };
  },
});
