import { readFile, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";

const VERSION = 1;
export const DEFAULT_NATIVE_PREFERENCES = Object.freeze({
  enabled: false,
  device_id: "default",
  exclusive: false,
  volume: 1,
});

export function normalizeNativePreferences(value) {
  if (!value || value.version !== VERSION) return { ...DEFAULT_NATIVE_PREFERENCES };
  if (typeof value.device_id !== "string" || value.device_id.length === 0 || value.device_id.length > 512 ||
    /[\u0000-\u001f\u007f]/.test(value.device_id)) return { ...DEFAULT_NATIVE_PREFERENCES };
  const deviceID = value.device_id;
  const volume = Number(value.volume);
  return {
    enabled: value.enabled === true,
    device_id: deviceID,
    exclusive: value.exclusive === true,
    volume: Number.isFinite(volume) && volume >= 0 && volume <= 1 ? volume : 1,
  };
}

export class NativePreferenceStore {
  #file;
  #value = { ...DEFAULT_NATIVE_PREFERENCES };
  #writes = Promise.resolve();

  constructor(directory) {
    this.#file = path.join(directory, "native-audio.json");
  }

  async load() {
    try {
      this.#value = normalizeNativePreferences(JSON.parse(await readFile(this.#file, "utf8")));
    } catch (error) {
      if (error?.code !== "ENOENT" && !(error instanceof SyntaxError)) throw error;
      this.#value = { ...DEFAULT_NATIVE_PREFERENCES };
    }
    return this.get();
  }

  get() { return { ...this.#value }; }

  async set(changes) {
    const next = normalizeNativePreferences({ version: VERSION, ...this.#value, ...changes });
    const previous = this.#value;
    this.#value = next;
    try {
      await this.#persist();
    } catch (error) {
      this.#value = previous;
      throw error;
    }
    return this.get();
  }

  async #persist() {
    const value = this.#value;
    const payload = `${JSON.stringify({ version: VERSION, ...value }, null, 2)}\n`;
    const temporary = `${this.#file}.next`;
    const write = async () => {
      try {
        await writeFile(temporary, payload, { encoding: "utf8", mode: 0o600 });
        await rename(temporary, this.#file);
      } finally {
        await rm(temporary, { force: true }).catch(() => {});
      }
    };
    this.#writes = this.#writes.then(write, write);
    await this.#writes;
  }
}
