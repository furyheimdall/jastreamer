import { expect, test } from "bun:test";
import { chmodSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

type CapturedProcess = {
  readonly environment: Readonly<Record<string, string>>;
  readonly stdin: string;
};

const captured = (path: string): CapturedProcess => {
  const value: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error("capture invalid");
  const record = Object.fromEntries(Object.entries(value));
  const environmentValue = record["environment"];
  if (typeof environmentValue !== "object" || environmentValue === null || Array.isArray(environmentValue) || typeof record["stdin"] !== "string") throw new Error("capture invalid");
  const environment = Object.fromEntries(Object.entries(environmentValue).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
  return { environment, stdin: record["stdin"] };
};

test("provider subprocesses receive only command-scoped credentials", async () => {
  // Given: fake gh/docker executables and ambient publication secrets in an isolated child.
  const root = mkdtempSync(join(tmpdir(), "publication-process-"));
  const script = `#!/usr/bin/env bun\nimport { writeFileSync } from "node:fs";\nconst stdin = await Bun.stdin.text();\nconst path = process.argv.at(-1);\nif (path === undefined) process.exit(2);\nwriteFileSync(path, JSON.stringify({ environment: process.env, stdin }));\nconsole.log(stdin);\n`;
  const gh = join(root, "gh"); const docker = join(root, "docker");
  await Promise.all([Bun.write(gh, script), Bun.write(docker, script)]); chmodSync(gh, 0o755); chmodSync(docker, 0o755);
  const ghCapture = join(root, "gh.json"); const loginCapture = join(root, "login.json"); const runCapture = join(root, "run.json");
  const program = `
    import { ProcessPublicationDriver } from "./tooling/release/publication-process.ts";
    const driver = new ProcessPublicationDriver(${JSON.stringify(join(root, "auth"))});
    const authorization = { kind: "publication-closure-sha256", sha256: "a".repeat(64) };
    const results = [];
    results.push(await driver.run({ id: "gh", phase: "read", argv: ["gh", "capture", ${JSON.stringify(ghCapture)}], stdin: "none", authorization }));
    results.push(await driver.run({ id: "login", phase: "read", argv: ["docker", "login", "ghcr.io", ${JSON.stringify(loginCapture)}], stdin: "github-token", authorization }));
    results.push(await driver.run({ id: "run", phase: "read", argv: ["docker", "run", ${JSON.stringify(runCapture)}], stdin: "none", authorization }));
    console.log(JSON.stringify(results));
  `;
  try {
    // When: the real process driver launches each provider command.
    const child = Bun.spawn(["bun", "-e", program], {
      cwd: process.cwd(), stdout: "pipe", stderr: "pipe",
      env: { PATH: `${root}:${process.env["PATH"] ?? ""}`, HOME: root, GH_TOKEN: "github-provider-token", PUBLICATION_RECEIPT_HMAC_KEY_B64: "receipt-secret", OTHER_SENTINEL_SECRET: "other-secret" },
    });
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
    expect({ exitCode, stderr }).toEqual({ exitCode: 0, stderr: "" });

    // Then: gh alone gets GH_TOKEN, Docker login gets it only via stdin, and output is redacted.
    const ghProcess = captured(ghCapture); const loginProcess = captured(loginCapture); const runProcess = captured(runCapture);
    expect(ghProcess.environment["GH_TOKEN"]).toBe("github-provider-token");
    expect(ghProcess.environment["DOCKER_CONFIG"]).toBeUndefined();
    expect(ghProcess.stdin).toBe("");
    expect(loginProcess.stdin).toBe("github-provider-token");
    expect(loginProcess.environment["DOCKER_CONFIG"]).toBe(join(root, "auth"));
    expect(runProcess.environment["DOCKER_CONFIG"]).toBe(join(root, "auth"));
    expect(runProcess.stdin).toBe("");
    for (const process of [ghProcess, loginProcess, runProcess]) {
      expect(process.environment["PUBLICATION_RECEIPT_HMAC_KEY_B64"]).toBeUndefined();
      expect(process.environment["OTHER_SENTINEL_SECRET"]).toBeUndefined();
    }
    expect(loginProcess.environment["GH_TOKEN"]).toBeUndefined();
    expect(runProcess.environment["GH_TOKEN"]).toBeUndefined();
    expect(stdout).not.toContain("github-provider-token");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
