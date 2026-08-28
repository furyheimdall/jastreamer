import { mkdirSync } from "node:fs";
import type { AuthorizedProviderCommand, ProviderResult, PublicationDriver } from "./publication-types";

export class ProviderConfigurationError extends Error {
  readonly name = "ProviderConfigurationError";
  constructor(readonly code: string) {
    super(code);
  }
}

const runtimeEnvironmentKeys = [
  "PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR",
  "XDG_RUNTIME_DIR", "SYSTEMROOT", "COMSPEC", "PATHEXT",
] as const;

export class ProcessPublicationDriver implements PublicationDriver {
  private readonly githubToken: string;

  constructor(private readonly dockerConfigRoot?: string) {
    const token = process.env["GH_TOKEN"];
    if (typeof token !== "string" || token.length === 0) throw new ProviderConfigurationError("GH_TOKEN_REQUIRED");
    this.githubToken = token;
    if (dockerConfigRoot !== undefined) mkdirSync(dockerConfigRoot, { recursive: true, mode: 0o700 });
  }

  private environment(executable: string): Record<string, string> {
    const environment: Record<string, string> = {};
    for (const key of runtimeEnvironmentKeys) {
      const value = process.env[key];
      if (value !== undefined) environment[key] = value;
    }
    if (executable === "gh") environment["GH_TOKEN"] = this.githubToken;
    if (executable === "docker" && this.dockerConfigRoot !== undefined) environment["DOCKER_CONFIG"] = this.dockerConfigRoot;
    return environment;
  }

  private redact(value: string): string {
    return value.replaceAll(this.githubToken, "[REDACTED]");
  }

  async run(command: AuthorizedProviderCommand): Promise<ProviderResult> {
    const executable = command.argv[0];
    const dockerOperation = executable === "docker" ? command.argv[1] : undefined;
    if ((executable !== "gh" && executable !== "docker") || command.argv.some((value) => value === "latest" || value.endsWith(":latest"))
      || dockerOperation === "build" || dockerOperation === "buildx" || dockerOperation === "push" || dockerOperation === "tag") throw new ProviderConfigurationError("PROVIDER_COMMAND_FORBIDDEN");
    const child = Bun.spawn([...command.argv], {
      env: this.environment(executable),
      stdin: command.stdin === "github-token" ? "pipe" : "ignore",
      stdout: "pipe",
      stderr: "pipe",
    });
    if (command.stdin === "github-token") {
      const sink = child.stdin;
      if (sink === undefined) throw new ProviderConfigurationError("PROVIDER_STDIN_UNAVAILABLE");
      sink.write(this.githubToken);
      sink.end();
    }
    const [exitCode, stdout, stderr] = await Promise.all([
      child.exited,
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
    ]);
    return { exitCode, stdout: this.redact(stdout), stderr: this.redact(stderr) };
  }
}
