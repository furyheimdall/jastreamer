import { spawn, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const repository = join(import.meta.dirname, "../..");
const serverRoot = join(repository, "apps/server");
const configPath = "../../tooling/fixtures/e2e/local.yaml";

const awaitExit = async (child) => {
  if (child.exitCode !== null) return;
  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("server shutdown timeout")), 10_000);
    child.once("exit", () => {
      clearTimeout(timeout);
      resolve();
    });
  });
};

export class AdminServerFixture {
  #child;
  #fixedAddress = "";

  constructor(resources) {
    this.directory = resources.directory;
    this.media = resources.media;
    this.secondRoot = resources.secondRoot;
    this.binary = resources.binary;
    this.origin = "";
    this.fingerprint = "";
  }

  static async create() {
    const directory = await mkdtemp(join(tmpdir(), "jastreamer-admin-restart-"));
    const media = join(directory, "music");
    const secondRoot = join(media, "archive");
    await mkdir(secondRoot, { recursive: true });
    await writeFile(join(secondRoot, "sample.wav"), "not media; scanner must report deterministically");
    const binary = join(directory, "jastreamer-server");
    const built = spawnSync("go", ["build", "-o", binary, "./cmd/jastreamer-server"], {
      cwd: serverRoot,
      encoding: "utf8",
    });
    if (built.status !== 0) {
      await rm(directory, { recursive: true, force: true });
      throw new Error(`server build failed: ${built.stderr}`);
    }
    return new AdminServerFixture({ directory, media, secondRoot, binary });
  }

  get running() {
    return this.#child?.exitCode === null;
  }

  async start({ setupSecret = "admin-restart-fixture-secret" } = {}) {
    if (this.running) throw new Error("server is already running");
    const environment = {
      ...process.env,
      JASTREAMER_DATA_DIR: this.directory,
      JASTREAMER_CATALOG_ROOT: this.media,
      JASTREAMER_ADDR: this.#fixedAddress || undefined,
      JASTREAMER_SETUP_SECRET: setupSecret || undefined,
    };
    const child = spawn(this.binary, ["--config", configPath], {
      cwd: serverRoot,
      env: environment,
      stdio: ["ignore", "pipe", "pipe"],
    });
    this.#child = child;
    let stderr = "";
    child.stderr.on("data", (chunk) => { stderr += chunk.toString(); });
    const ready = await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error(`server readiness timeout: ${stderr}`)), 30_000);
      child.once("exit", (code) => {
        clearTimeout(timeout);
        reject(new Error(`server exited ${code}: ${stderr}`));
      });
      child.stdout.on("data", (chunk) => {
        const match = chunk.toString().match(/ready (https:\/\/[^ ]+) fingerprint=([^\s]+)/);
        if (match?.[1] && match[2]) {
          clearTimeout(timeout);
          resolve({ origin: match[1], fingerprint: match[2] });
        }
      });
    });
    this.origin = ready.origin;
    this.fingerprint = ready.fingerprint;
    if (!this.#fixedAddress) this.#fixedAddress = new URL(this.origin).host;
    return ready;
  }

  async stop() {
    if (!this.#child || this.#child.exitCode !== null) return;
    this.#child.kill("SIGTERM");
    await awaitExit(this.#child);
    if (this.#child.exitCode !== 0) {
      throw new Error(`server shutdown failed: exit=${this.#child.exitCode} signal=${this.#child.signalCode}`);
    }
  }

  async restart() {
    await this.stop();
    return await this.start({ setupSecret: "" });
  }

  async cleanup() {
    await this.stop();
    await rm(this.directory, { recursive: true, force: true });
    return {
      processExited: !this.running,
      directoryRemoved: true,
      binaryRemoved: true,
      externalWrites: 0,
    };
  }
}
