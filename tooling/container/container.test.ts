import { afterEach, describe, expect, test } from "bun:test";
import { chmodSync, mkdirSync, readFileSync, renameSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { GateError, UsageError, parseArgs, scanContext } from "./args";
import { classifyDescriptors } from "./oci";
import { publishAtomically, removeWorkspace, workspaceIdentity } from "./qa";
import { cleanup, cleanupTargets, composeLoopbackEnvironment, createDataVolume, dataVolumeCreateArgs } from "./runtime";
import { containerRequestArguments, dockerEventArguments, parseWgetResponse } from "./process";
import type { Descriptor } from "./types";

const fixtureRoots = new Set<string>();
afterEach(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true });
  fixtureRoots.clear();
});

function rootFixture(): string {
  const root = `${process.env.TMPDIR ?? "/tmp"}/container-test-${process.pid}-${crypto.randomUUID()}`;
  fixtureRoots.add(root);
  mkdirSync(join(root, "apps/server"), { recursive: true }); mkdirSync(join(root, "packaging/container"), { recursive: true }); mkdirSync(join(root, "deploy"), { recursive: true });
  writeFileSync(join(root, "LICENSE"), "Apache-2.0\n");
  writeFileSync(join(root, "packaging/container/health-entrypoint.sh"), "#!/bin/sh\n");
  writeFileSync(join(root, "apps/server/VERSION"), "1.2.3\n"); writeFileSync(join(root, "apps/server/Dockerfile"), "COPY apps/server /src\n");
  writeFileSync(join(root, "apps/server/.dockerignore"), "apps/control\napps/renderer\n"); writeFileSync(join(root, "apps/server/Dockerfile.dockerignore"), "apps/control\napps/renderer\n");
  writeFileSync(join(root, "deploy/compose.yaml"), "services: {}\n"); return root;
}
describe("argument parser and context gate", () => {
  test("binds Compose QA and cleanup to explicit loopback identity", () => {
    expect(composeLoopbackEnvironment).toEqual({
      JASTREAMER_LAN_INTERFACE: "lo",
      JASTREAMER_ADVERTISED_ADDRESS: "127.0.0.1",
    });
  });
  test("accepts only the exact platform and scenario", () => {
    const root = rootFixture(); const args = ["--platform", "linux/amd64,linux/arm64", "--compose", "deploy/compose.yaml", "--scenario", "replacement-persistence", "--oci-layout", "/tmp/x.oci", "--output", "/tmp/x.json"];
    expect(parseArgs(args, root).version).toBe("1.2.3"); expect(() => parseArgs(args.with(1, "linux/arm64"), root)).toThrow(UsageError);
  });
  test("rejects a forbidden fixture before build", () => {
    const root = rootFixture(); mkdirSync(join(root, "fixture/flutter_assets"), { recursive: true }); writeFileSync(join(root, "fixture/flutter_assets/AssetManifest.json"), "{}");
    expect(() => scanContext(root, "fixture")).toThrow(new GateError("FORBIDDEN_CONTROL_ASSET"));
  });
});
test("index classification requires exactly two runnable target manifests", () => {
  const descriptor = (architecture: string, attestation = false): Descriptor => ({ mediaType: "application/vnd.oci.image.manifest.v1+json", digest: `sha256:${architecture.padEnd(64, "0")}`, size: 1,
    platform: { os: attestation ? "unknown" : "linux", architecture: attestation ? "unknown" : architecture }, annotations: attestation ? { "vnd.docker.reference.type": "attestation-manifest" } : undefined });
  expect(classifyDescriptors([descriptor("amd64"), descriptor("arm64"), descriptor("attestation", true)]).runnable).toHaveLength(2);
  expect(() => classifyDescriptors([descriptor("arm64")])).toThrow("RUNNABLE_MANIFEST_SET_INVALID");
});
test("publication rolls back every existing output on a promotion error", () => {
  const root = rootFixture(); const one = join(root, "one"); const two = join(root, "two"); const staged = join(root, "staged");
  writeFileSync(one, "old-one"); writeFileSync(two, "old-two"); writeFileSync(staged, "new-one");
  expect(() => publishAtomically([{ staged, final: one }, { staged: join(root, "missing"), final: two }])).toThrow();
  expect(readFileSync(one, "utf8")).toBe("old-one"); expect(readFileSync(two, "utf8")).toBe("old-two");
});
test("publication rolls back when final cleanup fails", () => {
  const root = rootFixture(); const final = join(root, "final"); const staged = join(root, "staged");
  writeFileSync(final, "old"); writeFileSync(staged, "new");
  expect(() => publishAtomically([{ staged, final }], () => { throw new Error("cleanup failed"); })).toThrow("cleanup failed");
  expect(readFileSync(final, "utf8")).toBe("old");
});
test("cleanup is scoped to names and Compose projects created by this run", () => {
  const commands = cleanupTargets(new Set(["jastreamer-task17-owned"]), new Set(["jastreamert17123"]), "/compose.yaml");
  expect(commands).toEqual([["rm", "-f", "jastreamer-task17-owned"], ["ps", "-aq", "--filter", "label=com.docker.compose.project=jastreamert17123"], ["compose", "-p", "jastreamert17123", "-f", "/compose.yaml", "down", "--remove-orphans"]]);
  expect(JSON.stringify(commands)).not.toContain("unrelated");
});
test("task data preparation uses a labeled Docker volume instead of a host bind mount", () => {
  expect(dataVolumeCreateArgs("jastreamer-task17-data-owned")).toEqual([
    "volume", "create", "--label", "io.jastreamer.qa=task17", "jastreamer-task17-data-owned",
  ]);
  expect(dataVolumeCreateArgs("jastreamer-task17-data-owned").join(" ")).not.toContain(":/");
  expect(readFileSync(new URL("./runtime.ts", import.meta.url), "utf8")).toContain("volume-nocopy");
});
test("privileged cleanup never follows a symlink root or mutates its external target", () => {
  // Given: an unremovable task-root symlink to an external directory with restrictive metadata.
  const root = rootFixture(); const parent = join(root, "readonly"); const external = join(root, "external");
  const work = join(parent, "owned-root"); const externalFile = join(external, "preserve");
  mkdirSync(parent); mkdirSync(external); writeFileSync(externalFile, "external");
  chmodSync(external, 0o700); chmodSync(externalFile, 0o600); symlinkSync(external, work, "dir"); chmodSync(parent, 0o500);

  // When: the exact production workspace cleanup is called.
  try { removeWorkspace(work); } catch (error) { if (!(error instanceof Error)) throw error; }

  // Then: cleanup has neither followed the link nor changed external ownership or mode.
  chmodSync(parent, 0o700);
  expect({ directory: statSync(external).mode & 0o777, file: statSync(externalFile).mode & 0o777 }).toEqual({ directory: 0o700, file: 0o600 });
  expect(readFileSync(externalFile, "utf8")).toBe("external");
});
test("cleanup rejects a task root replaced after identity capture", () => {
  // Given: identity was captured before the owned root was atomically replaced.
  const root = rootFixture(); const owned = join(root, "owned"); const original = join(root, "original");
  mkdirSync(owned); const identity = workspaceIdentity(owned); renameSync(owned, original); mkdirSync(owned); writeFileSync(join(owned, "external"), "preserve");

  // When/Then: cleanup rejects the replacement and preserves its descendant.
  expect(() => removeWorkspace(owned, identity)).toThrow("OWNED_WORKSPACE_REPLACED");
  expect(readFileSync(join(owned, "external"), "utf8")).toBe("preserve");
});
test.each([["SIGINT", 130], ["SIGTERM", 143]] as const)("%s after an owned-root event removes descendants and preserves signal exit", async (signal, expectedExit) => {
  // Given: a child that subscribes for termination before creating its task-owned root.
  const root = join(process.env.TMPDIR ?? "/tmp", `container-signal-${process.pid}-${crypto.randomUUID()}`);
  const child = Bun.spawn(["bun", new URL("./signal-fixture.ts", import.meta.url).pathname, root], { stdout: "pipe", stderr: "pipe" });
  const reader = child.stdout.getReader();
  const event = await Promise.race([
    reader.read(),
    new Promise<never>((_resolve, reject) => setTimeout(() => reject(new Error("OWNED_RESOURCE_EVENT_TIMEOUT")), 10_000)),
  ]);
  const ownedEvent = new TextDecoder().decode(event.value);
  expect(ownedEvent).toContain("OWNED_RESOURCES_CREATED");
  const volume = ownedEvent.trim().split(" ")[1];
  if (volume === undefined) throw new Error("OWNED_VOLUME_EVENT_INVALID");

  // When: termination is delivered twice after the exact owned-resource event.
  child.kill(signal); child.kill(signal);
  const code = await child.exited;

  // Then: cleanup is complete and the original signal exit is preserved.
  expect(code).toBe(expectedExit);
  expect(statSync(root, { throwIfNoEntry: false })).toBeUndefined();
  expect(Bun.spawnSync(["docker", "volume", "inspect", volume]).exitCode).not.toBe(0);
});
test("runtime fixture preparation contains no world-writable or privileged host cleanup contract", () => {
  const sources = ["runtime.ts", "qa.ts"].map((name) => readFileSync(new URL(`./${name}`, import.meta.url), "utf8")).join("\n");
  expect(sources).not.toMatch(/0o777|0777|a\+rw|o\s*=\s*rw|:\/cleanup/);
});
test("task volume grants UID 10001 persistence while denying an unrelated identity", () => {
  // Given: a uniquely named, task-owned Docker volume.
  const volume = `jastreamer-task17-test-${process.pid}-${crypto.randomUUID()}`; const resources = { names: new Set<string>(), projects: new Set<string>(), volumes: new Set<string>() };
  try {
    // When: production volume preparation initializes service-account ownership.
    createDataVolume(volume, resources.volumes, Buffer.from("fixture").toString("base64"));
    const image = "alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce";
    const denied = Bun.spawnSync(["docker", "run", "--rm", "--user", "10002:10002", "--mount", `type=volume,src=${volume},dst=/data`, image, "sh", "-ec", "touch /data/denied"]);
    const allowed = Bun.spawnSync(["docker", "run", "--rm", "--user", "10001:10001", "--mount", `type=volume,src=${volume},dst=/data`, image, "sh", "-ec", "touch /data/persisted"]);

    // Then: only the Server identity can persist data.
    expect(denied.exitCode).not.toBe(0);
    expect(allowed.exitCode).toBe(0);
  } finally { cleanup(resources, "/unused-compose.yaml"); }
});
test("cleanup removes later owned resources when an earlier Compose teardown fails", () => {
  // Given: an owned volume and a Compose project whose definition cannot be loaded.
  const volume = `jastreamer-task17-aggregate-${process.pid}-${crypto.randomUUID()}`;
  const resources = { names: new Set<string>(), projects: new Set([`jastreamert17aggregate${process.pid}`]), volumes: new Set<string>() };
  createDataVolume(volume, resources.volumes);

  // When: cleanup encounters the deterministic Compose error before volume teardown.
  let failure: Error | undefined;
  try { cleanup(resources, "/definitely/missing-compose.yaml"); } catch (error) {
    if (!(error instanceof Error)) throw error;
    failure = error;
  }

  // Then: the error is aggregated, while the later owned volume was still removed.
  expect(failure).toBeInstanceOf(AggregateError);
  expect(failure?.message).toBe("CONTAINER_CLEANUP_FAILED");
  expect(Bun.spawnSync(["docker", "volume", "inspect", volume]).exitCode).not.toBe(0);
});
test("Docker event subscription includes trigger-time history", () => {
  expect(dockerEventArguments(["container=unique", "event=health_status: healthy"], 1234)).toEqual([
    "events",
    "--since",
    "1234",
    "--format",
    "{{json .}}",
    "--filter",
    "container=unique",
    "--filter",
    "event=health_status: healthy",
  ]);
});
test("container-local HTTP transport preserves JSON requests and response status", () => {
  expect(containerRequestArguments("server-id", "/api/v1/bootstrap", "token", "POST", { name: "QA" })).toEqual([
    "exec",
    "server-id",
    "wget",
    "--no-check-certificate",
    "-S",
    "-O",
    "-",
    "-T",
    "10",
    "--header",
    "X-Jake-Protocol-Major: 2",
    "--header",
    "X-Jake-Supported-Protocol-Majors: 3,2",
    "--header",
    "Authorization: Bearer token",
    "--header",
    "Content-Type: application/json",
    "--post-data",
    "{\"name\":\"QA\"}",
    "https://127.0.0.1:8443/api/v1/bootstrap",
  ]);
  expect(parseWgetResponse(
    "{\"status\":\"ready\"}",
    "  HTTP/1.1 200 OK\n  Content-Type: application/json\n",
  )).toEqual({
    status: 200,
    body: { status: "ready" },
    text: "{\"status\":\"ready\"}",
    headers: "  HTTP/1.1 200 OK\n  Content-Type: application/json\n",
  });
});
test("container response parsing preserves a non-JSON portal body without fabricating JSON", () => {
  // Given: a successful HTML response from the pairing portal.
  // When: the shared response parser handles the non-JSON body.
  const response = parseWgetResponse("<html>pair</html>", "  HTTP/1.1 200 OK\n  Content-Type: text/html\n");

  // Then: text remains observable and the JSON projection is deterministically empty.
  expect(response).toEqual({ status: 200, body: {}, text: "<html>pair</html>", headers: "  HTTP/1.1 200 OK\n  Content-Type: text/html\n" });
});
