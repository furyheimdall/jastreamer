import { createHash, randomBytes } from "node:crypto";
import { chmodSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { httpsJSON, run, waitForDockerEvent } from "./process";
import type { CleanupFact, Platform, RuntimeFact } from "./types";

const sha256 = (path: string): string => createHash("sha256").update(readFileSync(path)).digest("hex");
const containerSha256 = (container: string, path: string): string => {
  const output = run("docker", ["exec", container, "sha256sum", path], { quiet: true });
  const digest = output.split(/\s+/, 1)[0];
  if (!digest || !/^[a-f0-9]{64}$/.test(digest)) throw new Error(`CONTAINER_DIGEST_INVALID ${path}`);
  return digest;
};
const archName = (platform: Platform): string => platform.endsWith("arm64") ? "aarch64" : "x86_64";
const canonicalArch = (value: string): "amd64" | "arm64" | "unknown" => value === "x86_64" || value === "amd64" ? "amd64" : value === "aarch64" || value === "arm64" ? "arm64" : "unknown";
export function importImage(rootfs: string, platform: Platform, tag: string): void {
  run("docker", ["import", "--platform", platform,
    "--change", "USER 10001:10001", "--change", "ENTRYPOINT [\"/usr/local/bin/jastreamer-server\"]",
    "--change", "WORKDIR /app", "--change", "ENV JASTREAMER_DATA_DIR=/var/lib/jastreamer",
    "--change", "HEALTHCHECK --interval=1s --timeout=5s --retries=30 CMD [\"/usr/local/bin/jastreamer-server\",\"health\"]",
    rootfs, tag], { quiet: true });
}
function config(directory: string): string {
  const value = { address: "127.0.0.1:8443", data_directory: "/var/lib/jastreamer", catalog_root: "/var/lib/jastreamer/catalog",
    catalog_migration: "/app/migrations/001_catalog.sql", playback_migration: "/app/migrations/002_playback.sql",
    playback_expansion: "/app/migrations/003_todo12.sql", certificate_dns: ["localhost"], certificate_ips: ["127.0.0.1"], pairing_ttl: "5m" };
  mkdirSync(directory, { recursive: true }); writeFileSync(join(directory, "server.json"), JSON.stringify(value, null, 2) + "\n"); return join(directory, "server.json");
}
async function apiFacts(token: string): Promise<Pick<RuntimeFact, "portal" | "health" | "productVersion" | "sourceRevision" | "contractRevision" | "catalogRevision">> {
  const health = await httpsJSON("https://127.0.0.1:8443/healthz");
  const portalResponse = await fetch("https://127.0.0.1:8443/pair/", { tls: { rejectUnauthorized: false } } as RequestInit & { tls: { rejectUnauthorized: boolean } });
  const discovery = await httpsJSON("https://127.0.0.1:8443/api/v1/discovery", token);
  if (health.status !== 200 || health.body.status !== "ready" || portalResponse.status !== 200 || !portalResponse.headers.get("content-type")?.includes("text/html") || discovery.status !== 200) throw new Error("RUNTIME_HTTP_CONTRACT_FAILED");
  return { health: String(health.body.status), portal: true, productVersion: String(discovery.body.product_version),
    sourceRevision: String(discovery.body.source_revision), contractRevision: String(discovery.body.contract_revision), catalogRevision: Number(discovery.body.catalog_revision) };
}
async function bootstrapAndPair(secret: string): Promise<{ admin: string; controller: string }> {
  const bootstrap = await httpsJSON("https://127.0.0.1:8443/api/v1/bootstrap", "", "POST", { setup_secret: secret, name: "Container QA Admin" });
  if (bootstrap.status !== 201) throw new Error(`BOOTSTRAP_FAILED ${bootstrap.status}`);
  const admin = String(bootstrap.body.token); const generated = await httpsJSON("https://127.0.0.1:8443/api/v1/pairing-codes", admin, "POST", {});
  const paired = await httpsJSON("https://127.0.0.1:8443/api/v1/pairings", "", "POST", { code: generated.body.code, name: "Container QA Controller" });
  if (generated.status !== 201 || paired.status !== 201) throw new Error("PAIRING_FAILED");
  return { admin, controller: String(paired.body.token) };
}
export async function runPlatform(platform: Platform, tag: string, workspace: string, names: Set<string>): Promise<RuntimeFact> {
  const suffix = `${process.pid}-${platform.split("/")[1]}`; const name = `jastreamer-task17-${suffix}`; names.add(name);
  const directory = join(workspace, suffix); const configDir = join(directory, "config"); const dataDir = join(directory, "data");
  config(configDir); mkdirSync(dataDir, { recursive: true }); chmodSync(dataDir, 0o777); const secret = randomBytes(24).toString("hex");
  const args = ["run", "-d", "--name", name, "--platform", platform, "--network", "host", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
    "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "-e", `JASTREAMER_SETUP_SECRET=${secret}`,
    "-v", `${configDir}:/etc/jastreamer:ro`, "-v", `${dataDir}:/var/lib/jastreamer`, tag, "--config", "/etc/jastreamer/server.json"];
  await waitForDockerEvent([`container=${name}`, "event=health_status: healthy"], () => { run("docker", args, { quiet: true }); });
  const hostArch = run("uname", ["-m"], { quiet: true }); const containerArch = run("docker", ["exec", name, "uname", "-m"], { quiet: true });
  const uid = run("docker", ["exec", name, "id", "-u"], { quiet: true }); const gid = run("docker", ["exec", name, "id", "-g"], { quiet: true });
  if (containerArch !== archName(platform) || uid !== "10001" || gid !== "10001") throw new Error(`RUNTIME_IDENTITY_FAILED ${platform}`);
  const hostCanonical = canonicalArch(hostArch); const containerCanonical = canonicalArch(containerArch);
  if (hostCanonical !== "arm64" || containerCanonical === "unknown") throw new Error(`HOST_ARCHITECTURE_UNSUPPORTED ${hostArch}`);
  const classification = hostCanonical === containerCanonical ? "native" : "qemu-emulated";
  if ((platform === "linux/arm64" && classification !== "native") || (platform === "linux/amd64" && classification !== "qemu-emulated")) throw new Error(`EXECUTION_CLASSIFICATION_INVALID ${platform}`);
  const tokens = await bootstrapAndPair(secret); const facts = await apiFacts(tokens.controller);
  run("docker", ["rm", "-f", name], { quiet: true });
  return { platform, classification, hostArch, containerArch, uid, gid, ...facts };
}

export async function runComposeReplacement(compose: string, image: string, workspace: string, projects: Set<string>): Promise<Record<string, unknown>> {
  const project = `jastreamert17${process.pid}`; projects.add(project); const base = join(workspace, "compose"); const configDir = join(base, "config"); const dataDir = join(base, "data");
  const configPath = config(configDir); const catalogDir = join(dataDir, "catalog");
  mkdirSync(catalogDir, { recursive: true });
  const fixture = resolve(dirname(fileURLToPath(import.meta.url)), "../fixtures/music/real.wav.b64");
  writeFileSync(join(catalogDir, "container-qa.wav"), Buffer.from(readFileSync(fixture, "utf8").trim(), "base64"));
  chmodSync(dataDir, 0o777); const secret = randomBytes(24).toString("hex");
  const env = { ...process.env, JASTREAMER_SERVER_IMAGE: image, JASTREAMER_CONFIG_PATH: configDir, JASTREAMER_DATA_PATH: dataDir, JASTREAMER_SETUP_SECRET: secret };
  const composeArgs = ["compose", "-p", project, "-f", compose];
  await waitForDockerEvent([`label=com.docker.compose.project=${project}`, "event=health_status: healthy"], () => { run("docker", [...composeArgs, "up", "-d"], { env, quiet: true }); });
  const firstID = run("docker", [...composeArgs, "ps", "-q", "jastreamer-server"], { env, quiet: true });
  const tokens = await bootstrapAndPair(secret);
  const scan = await httpsJSON("https://127.0.0.1:8443/api/v1/catalog/scans", tokens.admin, "POST", {});
  if (scan.status !== 202) throw new Error(`CATALOG_SCAN_FAILED ${scan.status}`);
  const before = await apiFacts(tokens.controller);
  const beforeCatalog = await httpsJSON("https://127.0.0.1:8443/api/v1/catalog/status", tokens.controller);
  const stateDigest = containerSha256(firstID, "/var/lib/jastreamer/security/state.json");
  const configDigest = sha256(configPath);
  await waitForDockerEvent([`label=com.docker.compose.project=${project}`, "event=health_status: healthy"], () => { run("docker", [...composeArgs, "up", "-d", "--force-recreate"], { env, quiet: true }); });
  const secondID = run("docker", [...composeArgs, "ps", "-q", "jastreamer-server"], { env, quiet: true }); const after = await apiFacts(tokens.controller);
  const afterCatalog = await httpsJSON("https://127.0.0.1:8443/api/v1/catalog/status", tokens.controller);
  const hostArch = run("uname", ["-m"], { quiet: true }); const containerArch = run("docker", ["exec", secondID, "uname", "-m"], { quiet: true });
  const classification = canonicalArch(hostArch) === canonicalArch(containerArch) ? "native" : "qemu-emulated";
  const invariant = {
    replaced: firstID !== secondID,
    classification,
    stateDigestStable: stateDigest === containerSha256(secondID, "/var/lib/jastreamer/security/state.json"),
    configDigestStable: configDigest === sha256(configPath),
    catalogRevisionBefore: before.catalogRevision,
    catalogRevisionAfter: after.catalogRevision,
    catalogTrackCountBefore: Number(beforeCatalog.body.track_count),
    catalogTrackCountAfter: Number(afterCatalog.body.track_count),
    analysisCompleteBefore: Number(beforeCatalog.body.analysis_complete),
    analysisCompleteAfter: Number(afterCatalog.body.analysis_complete),
    analysisQueuedBefore: Number(beforeCatalog.body.analysis_queued),
    analysisQueuedAfter: Number(afterCatalog.body.analysis_queued),
  };
  if (!invariant.replaced || invariant.classification !== "qemu-emulated" ||
    !invariant.stateDigestStable || !invariant.configDigestStable ||
    invariant.catalogRevisionBefore <= 0 ||
    invariant.catalogRevisionAfter < invariant.catalogRevisionBefore ||
    invariant.catalogTrackCountBefore <= 0 ||
    invariant.catalogTrackCountBefore !== invariant.catalogTrackCountAfter) {
    throw new Error(`REPLACEMENT_PERSISTENCE_FAILED ${JSON.stringify(invariant)}`);
  }
  run("docker", [...composeArgs, "down", "--remove-orphans"], { env, quiet: true });
  return { platform: "linux/amd64", classification, hostArch, containerArch, firstContainer: firstID.slice(0, 12), replacementContainer: secondID.slice(0, 12),
    controllerTokenUsableAfterReplacement: true, securityStateSha256Stable: true, configSha256Stable: true,
    catalogRevisionBefore: before.catalogRevision, catalogRevisionAfter: after.catalogRevision,
    catalogRevisionMonotonic: after.catalogRevision >= before.catalogRevision,
    catalogTrackCountBefore: Number(beforeCatalog.body.track_count), catalogTrackCountAfter: Number(afterCatalog.body.track_count),
    analysisCompleteBefore: Number(beforeCatalog.body.analysis_complete), analysisCompleteAfter: Number(afterCatalog.body.analysis_complete),
    analysisQueuedBefore: Number(beforeCatalog.body.analysis_queued), analysisQueuedAfter: Number(afterCatalog.body.analysis_queued) };
}

export function cleanupTargets(names: ReadonlySet<string>, projects: ReadonlySet<string>, compose: string): readonly (readonly string[])[] {
  const commands: (readonly string[])[] = [...names].map((name) => ["rm", "-f", name]);
  commands.push(...[...projects].map((project) => ["ps", "-aq", "--filter", `label=com.docker.compose.project=${project}`]));
  commands.push(...[...projects].map((project) => ["compose", "-p", project, "-f", compose, "down", "--remove-orphans"]));
  return commands;
}
export function cleanup(names: Set<string>, projects: Set<string>, compose: string): CleanupFact {
  const failures: Error[] = [];
  for (const name of names) {
    const ids = run("docker", ["ps", "-aq", "--filter", `name=^/${name}$`], { quiet: true }).split("\n").filter(Boolean);
    if (ids.length) try { run("docker", ["rm", "-f", ...ids], { quiet: true }); } catch (error) { failures.push(error as Error); }
  }
  const env = { ...process.env, JASTREAMER_SETUP_SECRET: "cleanup-only", JASTREAMER_CONFIG_PATH: "/tmp/jastreamer-cleanup-config", JASTREAMER_DATA_PATH: "/tmp/jastreamer-cleanup-data" };
  for (const project of projects) {
    const ids = run("docker", ["ps", "-aq", "--filter", `label=com.docker.compose.project=${project}`], { quiet: true }).split("\n").filter(Boolean);
    if (ids.length) try { run("docker", ["rm", "-f", ...ids], { quiet: true }); } catch (error) { failures.push(error as Error); }
    try { run("docker", ["compose", "-p", project, "-f", compose, "down", "--remove-orphans"], { env, quiet: true }); } catch (error) { failures.push(error as Error); }
  }
  const taskContainersRemoved = [...names].every((name) =>
    !run("docker", ["ps", "-aq", "--filter", `name=^/${name}$`], { quiet: true }));
  const projectResources = (kind: "ps" | "network" | "volume", project: string): string => {
    const command = kind === "ps" ? ["ps", "-aq"] : [kind, "ls", "-q"];
    return run("docker", [...command, "--filter", `label=com.docker.compose.project=${project}`], { quiet: true });
  };
  const composeContainersRemoved = [...projects].every((project) => !projectResources("ps", project));
  const composeNetworksRemoved = [...projects].every((project) => !projectResources("network", project));
  const composeVolumesRemoved = [...projects].every((project) => !projectResources("volume", project));
  if (!taskContainersRemoved || !composeContainersRemoved || !composeNetworksRemoved || !composeVolumesRemoved) {
    failures.push(new Error("OWNED_DOCKER_RESOURCES_REMAIN"));
  }
  if (failures.length) throw new AggregateError(failures, "CONTAINER_CLEANUP_FAILED");
  return { taskContainersRemoved, composeContainersRemoved, composeNetworksRemoved, composeVolumesRemoved };
}
