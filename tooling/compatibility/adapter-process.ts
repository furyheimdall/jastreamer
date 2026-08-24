import { createHash } from "node:crypto";
import {
  cpSync,
  mkdtempSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { CompatibilityError } from "./parser";
import type { Artifact, Cell } from "./parser";

export type CommandResult = {
  readonly exitCode: number;
  readonly stdout: string;
  readonly stderr: string;
};

export type ComponentEvidence = {
  readonly status: "passed" | "unsupported";
  readonly trace: readonly string[];
  readonly assertions: readonly string[];
};

export type AdapterEvidence = ComponentEvidence & {
  readonly candidateSha256: string;
};

export const flutterImage =
  "ghcr.io/cirruslabs/flutter:3.35.0@sha256:114f14a7cf973b08e4607d3e2fb4a3b2dc977c08877e651743f8cbed0e971046";

export const run = (
  command: readonly string[],
  options: { readonly cwd?: string } = {},
): CommandResult => {
  const result = Bun.spawnSync(command, {
    cwd: options.cwd,
    stdout: "pipe",
    stderr: "pipe",
  });
  return {
    exitCode: result.exitCode,
    stdout: new TextDecoder().decode(result.stdout),
    stderr: new TextDecoder().decode(result.stderr),
  };
};

export const checked = (
  command: readonly string[],
  options: { readonly cwd?: string } = {},
): CommandResult => {
  const result = run(command, options);
  if (result.exitCode !== 0)
    throw new CompatibilityError(
      `adapter command failed (${result.exitCode}): ${result.stderr.trim()}`,
    );
  return result;
};

export const jsonOutput = (
  result: CommandResult,
  adapter: string,
): unknown => {
  try {
    return JSON.parse(result.stdout);
  } catch {
    throw new CompatibilityError(`${adapter} emitted invalid JSON`);
  }
};

const digest = (paths: readonly string[]): string => {
  const hash = createHash("sha256");
  for (const path of paths) hash.update(readFileSync(path));
  return hash.digest("hex");
};

export const orderTrace = (
  cell: Cell,
  subject: Artifact,
  peer: Artifact,
): readonly string[] =>
  cell.order === "old-first"
    ? [
        `start-peer:${peer.sentinel}`,
        `start-candidate:${subject.adapter}`,
        "negotiate",
        "execute-wire",
      ]
    : [
        `start-candidate:${subject.adapter}`,
        "candidate-ready-without-peer",
        `start-peer:${peer.sentinel}`,
        "reconnect",
        "negotiate",
        "execute-wire",
      ];

export class CandidateBuilds {
  static prepare(
    workspaceRoot: string,
    fixtureRoot: string,
  ): CandidateBuilds {
    const temporaryRoot = mkdtempSync(join(tmpdir(), "jstreamer-compat-"));
    const controlRoot = join(temporaryRoot, "control");
    const controlBinary = join(
      controlRoot,
      ".compat",
      "control-adapter",
    );
    const serverBinary = join(temporaryRoot, "jstreamer-compat-server");
    const rendererTarget = join(temporaryRoot, "renderer-target");
    const rendererBinary = join(
      rendererTarget,
      "release",
      "jstreamer-renderer",
    );
    try {
      cpSync(resolve(workspaceRoot, "apps/control"), controlRoot, {
        recursive: true,
        filter: (source: string) =>
          ![".dart_tool", "build"].includes(basename(source)),
      });
      checked(
        [
          "go",
          "build",
          "-o",
          serverBinary,
          "./cmd/jstreamer-compat",
        ],
        { cwd: resolve(workspaceRoot, "apps/server") },
      );
      checked(
        [
          "cargo",
          "build",
          "--release",
          "--target-dir",
          rendererTarget,
        ],
        { cwd: resolve(workspaceRoot, "apps/renderer") },
      );
      checked([
        "docker",
        "run",
        "--rm",
        "--platform",
        "linux/amd64",
        "-v",
        `${controlRoot}:/workspace`,
        "-w",
        "/workspace",
        flutterImage,
        "sh",
        "-lc",
        "flutter pub get >/dev/null && mkdir -p .compat && dart compile exe bin/compatibility_adapter.dart -o .compat/control-adapter",
      ]);
      return new CandidateBuilds({
        temporaryRoot,
        fixtureRoot,
        controlRoot,
        controlBinary,
        serverBinary,
        rendererBinary,
      });
    } catch (error) {
      CandidateBuilds.removeTemporary(temporaryRoot, controlRoot);
      throw error;
    }
  }

  private static removeTemporary(
    temporaryRoot: string,
    controlRoot: string,
  ): void {
    if (controlRoot.startsWith(`${temporaryRoot}/`)) {
      run([
        "docker",
        "run",
        "--rm",
        "-v",
        `${controlRoot}:/workspace`,
        "alpine:3.22",
        "rm",
        "-rf",
        "/workspace/.dart_tool",
        "/workspace/build",
        "/workspace/.compat",
      ]);
    }
    rmSync(temporaryRoot, { recursive: true, force: true });
  }

  readonly fixtureRoot: string;
  readonly controlRoot: string;
  readonly controlBinary: string;
  readonly serverBinary: string;
  readonly rendererBinary: string;
  readonly hashes: Readonly<Record<Artifact["component"], string>>;
  private readonly temporaryRoot: string;

  private constructor(values: {
    readonly temporaryRoot: string;
    readonly fixtureRoot: string;
    readonly controlRoot: string;
    readonly controlBinary: string;
    readonly serverBinary: string;
    readonly rendererBinary: string;
  }) {
    this.temporaryRoot = values.temporaryRoot;
    this.fixtureRoot = values.fixtureRoot;
    this.controlRoot = values.controlRoot;
    this.controlBinary = values.controlBinary;
    this.serverBinary = values.serverBinary;
    this.rendererBinary = values.rendererBinary;
    this.hashes = {
      control: digest([this.controlBinary]),
      server: digest([this.serverBinary]),
      renderer: digest([this.rendererBinary]),
    };
  }

  dispose(): void {
    CandidateBuilds.removeTemporary(
      this.temporaryRoot,
      this.controlRoot,
    );
  }
}
