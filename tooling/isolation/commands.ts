import { appendFileSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { filteredEnvironment } from "./io.ts";
import type { CommandReceipt, ComponentName } from "./types.ts";

const FLUTTER_IMAGE = "ghcr.io/cirruslabs/flutter:3.35.0@sha256:114f14a7cf973b08e4607d3e2fb4a3b2dc977c08877e651743f8cbed0e971046";
type Command = { readonly argv: readonly string[]; readonly display: string };
type CommandContext = {
  readonly component: ComponentName;
  readonly worktree: string;
  readonly namespaceRoot: string;
  readonly artifactRoot: string;
  readonly tracePath: string;
};
export type CommandExecution = {
  readonly commands: readonly CommandReceipt[];
  readonly derivedImageCleanup: "removed" | "not_applicable" | "failed";
};

class CommandSetupError extends Error {
  override readonly name = "CommandSetupError";
  constructor(readonly detail: string) { super(detail); }
}

const quote = (value: string): string => `'${value.replaceAll("'", `'"'"'`)}'`;

const buildControlImage = (): { readonly image: string; readonly receipt: CommandReceipt } => {
  const image = `jstreamer-isolation-flutter:${process.pid}-${crypto.randomUUID()}`;
  const dockerfile = `FROM ${FLUTTER_IMAGE}\nUSER root\nRUN apt-get update && apt-get install -y --no-install-recommends strace && rm -rf /var/lib/apt/lists/*\n`;
  const context = mkdtempSync(join(tmpdir(), "isolation-docker-context-"));
  try {
    const result = Bun.spawnSync(["docker", "build", "--quiet", "--tag", image, "--file", "-", context], {
      stdin: Buffer.from(dockerfile), stdout: "inherit", stderr: "inherit",
    });
    return { image, receipt: { command: `docker build strace image FROM ${FLUTTER_IMAGE}`, exitCode: result.exitCode } };
  } finally {
    rmSync(context, { recursive: true, force: true });
  }
};

const dockerCommand = (context: CommandContext, image: string, operation: readonly string[], index: number): Command => {
  const pubCache = join(context.namespaceRoot, context.component, "cache", "pub");
  const traceDirectory = join(dirname(context.tracePath), "control-inner");
  const owner = `${process.getuid?.() ?? 1000}:${process.getgid?.() ?? 1000}`;
  const innerTrace = `/traces/control-${index}.strace`;
  const shellCommand = `trap 'chown -R ${owner} /workspace /pub-cache /artifacts /traces' EXIT; strace -f -yy -s 4096 -e trace=%file,%process -o ${innerTrace} ${operation.map(quote).join(" ")}`;
  const argv = ["docker", "run", "--rm", "-e", "HOME=/tmp", "-e", "PUB_CACHE=/pub-cache", "-e", "GIT_CONFIG_COUNT=1",
    "-e", "GIT_CONFIG_KEY_0=safe.directory", "-e", "GIT_CONFIG_VALUE_0=/sdks/flutter", "-v", `${context.worktree}:/workspace`,
    "-v", `${pubCache}:/pub-cache`, "-v", `${context.artifactRoot}:/artifacts`, "-v", `${traceDirectory}:/traces`,
    "-w", "/workspace/apps/control", image, "sh", "-lc", shellCommand];
  return { argv, display: operation.join(" ") };
};

const commandsFor = (context: CommandContext, controlImage?: string): readonly Command[] => {
  switch (context.component) {
    case "server":
      return [
        { argv: ["go", "test", "./..."], display: "CGO_ENABLED=0 go test ./..." },
        { argv: ["env", "CGO_ENABLED=1", "go", "test", "-race", "./..."], display: "CGO_ENABLED=1 go test -race ./..." },
        { argv: ["go", "vet", "./..."], display: "CGO_ENABLED=0 go vet ./..." },
        { argv: ["go", "build", "-o", join(context.artifactRoot, "jstreamer-server"), "./cmd/jstreamer-server"], display: "CGO_ENABLED=0 go build -o <artifact>/jstreamer-server ./cmd/jstreamer-server" },
      ];
    case "renderer": {
      const artifact = quote(join(context.artifactRoot, "jstreamer-renderer"));
      return [
        { argv: ["cargo", "test", "--locked"], display: "cargo test --locked" },
        { argv: ["cargo", "clippy", "--locked", "--all-targets", "--all-features", "--", "-D", "warnings"], display: "cargo clippy --locked --all-targets --all-features -- -D warnings" },
        { argv: ["sh", "-lc", `cargo build --release --locked && install -m 0755 "$CARGO_TARGET_DIR/release/jstreamer-renderer" ${artifact}`], display: "cargo build --release --locked && install <release>/jstreamer-renderer <artifact>" },
      ];
    }
    case "control": {
      if (controlImage === undefined) throw new CommandSetupError("Control trace image is missing");
      const operations = [
        ["flutter", "pub", "get", "--enforce-lockfile"], ["dart", "format", "--output=none", "--set-exit-if-changed", "."],
        ["flutter", "analyze"], ["flutter", "test"], ["flutter", "build", "web", "--release", "--output", "/artifacts/web"],
      ];
      return operations.map((operation, index) => dockerCommand(context, controlImage, operation, index));
    }
  }
};

export function executeCommands(context: CommandContext): CommandExecution {
  mkdirSync(dirname(context.tracePath), { recursive: true });
  mkdirSync(join(dirname(context.tracePath), "control-inner"), { recursive: true });
  const env = filteredEnvironment(context.component, context.namespaceRoot, process.env);
  for (const path of [env["HOME"], env["GOCACHE"], env["GOMODCACHE"], env["CARGO_HOME"], env["CARGO_TARGET_DIR"], env["PUB_CACHE"]]) {
    if (path !== undefined) mkdirSync(path, { recursive: true });
  }
  const setup = context.component === "control" ? buildControlImage() : undefined;
  const receipts: CommandReceipt[] = setup === undefined ? [] : [setup.receipt];
  let imageCleanup: CommandExecution["derivedImageCleanup"] = setup === undefined ? "not_applicable" : "failed";
  try {
    if (setup?.receipt.exitCode === 0 || setup === undefined) {
      let append = false;
      for (const command of commandsFor(context, setup?.image)) {
        const argv: string[] = context.component === "control" ? [...command.argv] : [
          "strace", "-f", "-yy", "-s", "4096", "-e", "trace=%file,%process", ...(append ? ["-A"] : []), "-o", context.tracePath, ...command.argv,
        ];
        const result = Bun.spawnSync(argv, { cwd: join(context.worktree, "apps", context.component), env, stdout: "inherit", stderr: "inherit" });
        receipts.push({ command: command.display, exitCode: result.exitCode });
        append = true;
      }
      if (context.component === "control") {
        const inner = join(dirname(context.tracePath), "control-inner");
        const files = readdirSync(inner).filter((file) => file.endsWith(".strace")).sort();
        if (files.length !== 5) throw new CommandSetupError(`expected 5 Control traces, received ${files.length}`);
        for (const file of files) appendFileSync(context.tracePath, readFileSync(join(inner, file)));
      }
    }
  } finally {
    if (setup !== undefined) imageCleanup = Bun.spawnSync(["docker", "image", "rm", "--force", setup.image], { stdout: "ignore", stderr: "ignore" }).exitCode === 0 ? "removed" : "failed";
  }
  return { commands: receipts, derivedImageCleanup: imageCleanup };
}
