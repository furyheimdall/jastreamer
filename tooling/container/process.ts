import { spawn, spawnSync } from "node:child_process";

export function run(command: string, args: readonly string[], options: { cwd?: string; env?: NodeJS.ProcessEnv; quiet?: boolean } = {}): string {
  const result = spawnSync(command, args, { cwd: options.cwd, env: options.env, encoding: "utf8", stdio: options.quiet ? "pipe" : ["ignore", "pipe", "inherit"] });
  if (result.status !== 0) throw new Error(`${command} failed (${result.status ?? "signal"}): ${(result.stderr || result.stdout || "").trim()}`);
  return result.stdout.trim();
}

export const dockerEventArguments = (
  filters: readonly string[],
  since: number,
): readonly string[] => [
  "events",
  "--since",
  String(since),
  "--format",
  "{{json .}}",
  ...filters.flatMap((value) => ["--filter", value]),
];

type ContainerResponse = {
  readonly status: number;
  readonly body: Record<string, unknown>;
  readonly text: string;
  readonly headers: string;
};

export const containerRequestArguments = (
  container: string,
  path: string,
  token = "",
  method = "GET",
  body?: unknown,
): readonly string[] => {
  const args = [
    "exec",
    container,
    "wget",
    "--no-check-certificate",
    "-S",
    "-O",
    "-",
    "-T",
    "10",
    "--header",
    "X-Jake-Protocol-Major: 2",
  ];
  if (token) args.push("--header", `Authorization: Bearer ${token}`);
  if (body !== undefined) {
    if (method !== "POST") throw new Error(`CONTAINER_HTTP_METHOD_UNSUPPORTED ${method}`);
    args.push("--header", "Content-Type: application/json", "--post-data", JSON.stringify(body));
  }
  args.push(`https://127.0.0.1:8443${path}`);
  return args;
};

export const parseWgetResponse = (stdout: string, stderr: string): ContainerResponse => {
  const statuses = [...stderr.matchAll(/HTTP\/\d(?:\.\d)?\s+(\d{3})/g)];
  const statusValue = statuses.at(-1)?.[1];
  if (statusValue === undefined) throw new Error(`CONTAINER_HTTP_STATUS_MISSING ${stderr.trim()}`);
  const text = stdout.trim();
  let body: Record<string, unknown> = {};
  try {
    body = JSON.parse(text) as Record<string, unknown>;
  } catch {
    // Non-JSON surfaces such as the pairing portal are validated by the caller.
  }
  return { status: Number(statusValue), body, text, headers: stderr };
};

export const containerJSON = (
  container: string,
  path: string,
  token = "",
  method = "GET",
  body?: unknown,
): ContainerResponse => {
  const result = spawnSync("docker", containerRequestArguments(container, path, token, method, body), {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  return parseWgetResponse(result.stdout, result.stderr);
};

export async function waitForDockerEvent(filters: readonly string[], trigger: () => void, timeoutMs = 90_000): Promise<void> {
  const child = spawn("docker", dockerEventArguments(filters, Math.floor(Date.now() / 1000)), { stdio: ["ignore", "pipe", "pipe"] });
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
