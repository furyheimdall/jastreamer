import { spawn, spawnSync } from "node:child_process";

export function run(command: string, args: readonly string[], options: { cwd?: string; env?: NodeJS.ProcessEnv; quiet?: boolean } = {}): string {
  const result = spawnSync(command, args, { cwd: options.cwd, env: options.env, encoding: "utf8", stdio: options.quiet ? "pipe" : ["ignore", "pipe", "inherit"] });
  if (result.status !== 0) throw new Error(`${command} failed (${result.status ?? "signal"}): ${(result.stderr || result.stdout || "").trim()}`);
  return result.stdout.trim();
}

export async function waitForDockerEvent(filters: readonly string[], trigger: () => void, timeoutMs = 90_000): Promise<void> {
  const child = spawn("docker", ["events", "--format", "{{json .}}", ...filters.flatMap((value) => ["--filter", value])], { stdio: ["ignore", "pipe", "pipe"] });
  let settled = false;
  const completion = new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => { child.kill("SIGKILL"); reject(new Error(`timed out awaiting Docker event: ${filters.join(",")}`)); }, timeoutMs);
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", () => { if (!settled) { settled = true; clearTimeout(timer); child.kill("SIGTERM"); resolve(); } });
    child.once("error", (error) => { clearTimeout(timer); reject(error); });
    child.once("exit", (code) => { if (!settled && code !== null && code !== 0) { clearTimeout(timer); reject(new Error(`docker events exited ${code}`)); } });
  });
  await new Promise<void>((resolve, reject) => { child.once("spawn", resolve); child.once("error", reject); });
  trigger();
  await completion;
}

export async function httpsJSON(url: string, token = "", method = "GET", body?: unknown): Promise<{ status: number; body: Record<string, unknown>; text: string }> {
  const headers: Record<string, string> = { "X-Jake-Protocol-Major": "2" };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const init = { method, headers, body: body === undefined ? undefined : JSON.stringify(body), tls: { rejectUnauthorized: false } } as RequestInit & { tls: { rejectUnauthorized: boolean } };
  const response = await fetch(url, init);
  const text = await response.text();
  let parsed: Record<string, unknown> = {}; try { parsed = JSON.parse(text) as Record<string, unknown>; } catch { /* caller validates status/text */ }
  return { status: response.status, body: parsed, text };
}
