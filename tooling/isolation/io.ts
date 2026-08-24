import { mkdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { ComponentName } from "./types.ts";

const SAFE_ENVIRONMENT_KEYS = ["PATH", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR"] as const;

export function filteredEnvironment(component: ComponentName, namespaceRoot: string, ambient: Readonly<Record<string, string | undefined>>): Record<string, string> {
  const result: Record<string, string> = {};
  for (const key of SAFE_ENVIRONMENT_KEYS) {
    const value = ambient[key];
    if (value !== undefined) result[key] = value;
  }
  const cache = join(namespaceRoot, component, "cache");
  const rustupHome = ambient["RUSTUP_HOME"] ?? (ambient["HOME"] === undefined ? undefined : join(ambient["HOME"], ".rustup"));
  const environment: Record<string, string> = {
    ...result,
    HOME: join(namespaceRoot, component, "home"),
    XDG_CACHE_HOME: join(cache, "xdg"),
    GOCACHE: join(cache, "go-build"),
    GOMODCACHE: join(cache, "go-mod"),
    CARGO_HOME: join(cache, "cargo-home"),
    CARGO_TARGET_DIR: join(cache, "cargo-target"),
    PUB_CACHE: join(cache, "pub"),
    CGO_ENABLED: "0",
    ISOLATION_COMPONENT: component,
    ISOLATION_CANARY: `todo16-${component}-${process.pid}`,
  };
  if (rustupHome !== undefined) environment["RUSTUP_HOME"] = rustupHome;
  return environment;
}

export function atomicWriteJson(path: string, value: unknown): void {
  mkdirSync(dirname(path), { recursive: true });
  const temporary = join(dirname(path), `.${path.split("/").at(-1) ?? "output"}.tmp-${process.pid}-${crypto.randomUUID()}`);
  try {
    writeFileSync(temporary, `${JSON.stringify(value, null, 2)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
    renameSync(temporary, path);
  } catch (error) {
    rmSync(temporary, { force: true });
    throw error;
  }
}
