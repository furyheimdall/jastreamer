import { constants as fsConstants } from "node:fs";
import { access, mkdir, open, readFile, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { normalizeEndpoint, normalizeServerId } from "./security.mjs";

export const MAX_RECENT_SERVERS = 12;
const STORE_VERSION = 1;
const MAX_TIMESTAMP = 8_640_000_000_000_000;

export function resolveUserDataPath({ isPackaged, executablePath, appDirectory }) {
  return isPackaged
    ? path.join(path.dirname(executablePath), "user-data")
    : path.join(appDirectory, ".user-data");
}

export async function ensureWritableDirectory(directory) {
  await mkdir(directory, { recursive: true, mode: 0o700 });
  await access(directory, fsConstants.R_OK | fsConstants.W_OK);

  const marker = path.join(directory, `.write-check-${process.pid}-${Date.now()}`);
  const handle = await open(marker, "wx", 0o600);
  try {
    await handle.writeFile("ok", "utf8");
    await handle.sync();
  } finally {
    await handle.close();
    await rm(marker, { force: true });
  }
}

function cleanDisplayText(value, maximum) {
  if (typeof value !== "string") return null;
  const text = value.trim();
  return text && text.length <= maximum && !/[\u0000-\u001f\u007f]/.test(text) ? text : null;
}

export function normalizeRecent(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  try {
    const id = normalizeServerId(value.id);
    const origin = normalizeEndpoint(value.origin, { allowImplicitHttp: false });
    const name = cleanDisplayText(value.name, 128);
    const version = cleanDisplayText(value.version, 64);
    const lastUsed = Number(value.lastUsed);
    if (!name || !version || !Number.isFinite(lastUsed) || lastUsed < 0 || lastUsed > MAX_TIMESTAMP) {
      return null;
    }
    return { id, origin, name, version, lastUsed: Math.trunc(lastUsed) };
  } catch {
    return null;
  }
}

export class RecentServerStore {
  #filePath;
  #recents = [];
  #writeChain = Promise.resolve();

  constructor(directory) {
    this.#filePath = path.join(directory, "recents.json");
  }

  async load() {
    let parsed;
    try {
      parsed = JSON.parse(await readFile(this.#filePath, "utf8"));
    } catch (error) {
      if (error?.code === "ENOENT") {
        this.#recents = [];
        return this.list();
      }
      if (error instanceof SyntaxError) {
        throw new Error("최근 서버 목록 파일이 손상되었습니다.", { cause: error });
      }
      throw error;
    }

    if (!parsed || parsed.version !== STORE_VERSION || !Array.isArray(parsed.recents)) {
      throw new Error("최근 서버 목록 파일 형식을 읽을 수 없습니다.");
    }

    const unique = new Map();
    for (const item of parsed.recents) {
      const recent = normalizeRecent(item);
      if (!recent) continue;
      const key = `${recent.id}\0${recent.origin}`;
      const previous = unique.get(key);
      if (!previous || recent.lastUsed > previous.lastUsed) unique.set(key, recent);
    }
    this.#recents = [...unique.values()]
      .sort((left, right) => right.lastUsed - left.lastUsed)
      .slice(0, MAX_RECENT_SERVERS);
    return this.list();
  }

  list() {
    return this.#recents.map((recent) => ({ ...recent }));
  }

  async upsert(server) {
    const recent = normalizeRecent({ ...server, lastUsed: Date.now() });
    if (!recent) throw new Error("저장할 서버 정보가 올바르지 않습니다.");

    const key = `${recent.id}\0${recent.origin}`;
    this.#recents = [
      recent,
      ...this.#recents.filter((entry) => `${entry.id}\0${entry.origin}` !== key),
    ].slice(0, MAX_RECENT_SERVERS);
    await this.#persist();
    return this.list();
  }

  async #persist() {
    const payload = `${JSON.stringify({ version: STORE_VERSION, recents: this.#recents }, null, 2)}\n`;
    const temporaryPath = `${this.#filePath}.next`;

    const write = async () => {
      try {
        await writeFile(temporaryPath, payload, { encoding: "utf8", mode: 0o600 });
        await rename(temporaryPath, this.#filePath);
      } finally {
        await rm(temporaryPath, { force: true }).catch(() => {});
      }
    };
    this.#writeChain = this.#writeChain.then(write, write);
    await this.#writeChain;
  }
}
