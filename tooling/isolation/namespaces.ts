import { mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { COMPONENTS, type CanaryEvidence, type ComponentName } from "./types.ts";

export type NamespaceSet = Readonly<Record<ComponentName, {
  readonly root: string;
  readonly cache: string;
  readonly secret: string;
  readonly artifact: string;
  readonly evidence: CanaryEvidence;
}>>;

export class NamespaceError extends Error {
  override readonly name = "NamespaceError";
  constructor() { super("namespace construction failed"); }
}

export function createNamespaces(runRoot: string): NamespaceSet {
  const paths = new Set<string>();
  const tokens = new Set<string>();
  const entries = new Map<ComponentName, NamespaceSet[ComponentName]>();
  for (const component of COMPONENTS) {
    const root = join(runRoot, "namespaces", component);
    const cache = join(root, "cache");
    const secret = join(root, "secret");
    const artifact = join(root, "artifact");
    const token = `todo16-${component}-${crypto.randomUUID()}`;
    const componentPaths = [cache, secret, artifact];
    const collisionFree = componentPaths.every((path) => !paths.has(path)) && !tokens.has(token);
    for (const path of componentPaths) { paths.add(path); mkdirSync(path, { recursive: true }); }
    tokens.add(token);
    const cachePath = join(cache, "canary");
    const secretPath = join(secret, "canary");
    const artifactPath = join(artifact, "canary");
    for (const path of [cachePath, secretPath, artifactPath]) writeFileSync(path, `${token}\n`, { mode: 0o600 });
    const isolated = [cache, secret, artifact].every((path) => readdirSync(path).length === 1);
    entries.set(component, { root, cache, secret, artifact, evidence: {
      cachePath, secretPath, artifactPath, token, collisionFree: collisionFree && isolated,
    } });
  }
  const server = entries.get("server");
  const control = entries.get("control");
  const renderer = entries.get("renderer");
  if (server === undefined || control === undefined || renderer === undefined) throw new NamespaceError();
  return { server, control, renderer };
}

export function verifyCanary(component: ComponentName, namespaces: NamespaceSet): CanaryEvidence {
  const own = namespaces[component].evidence;
  const ownPaths = [own.cachePath, own.secretPath, own.artifactPath];
  const allPaths = COMPONENTS.flatMap((name) => {
    const evidence = namespaces[name].evidence;
    return [evidence.cachePath, evidence.secretPath, evidence.artifactPath];
  });
  const uniquePaths = new Set(allPaths).size === allPaths.length;
  const ownIntact = ownPaths.every((path) => readFileSync(path, "utf8") === `${own.token}\n`);
  const foreignClean = COMPONENTS.filter((name) => name !== component).every((name) => {
    const evidence = namespaces[name].evidence;
    return [evidence.cachePath, evidence.secretPath, evidence.artifactPath]
      .every((path) => readFileSync(path, "utf8") !== `${own.token}\n`);
  });
  return { ...own, collisionFree: uniquePaths && ownIntact && foreignClean };
}
