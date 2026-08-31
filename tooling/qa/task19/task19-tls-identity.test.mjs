import { X509Certificate } from "node:crypto";
import { afterEach, describe, expect, test } from "bun:test";
import { access, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createEphemeralTlsIdentity } from "./task19-tls-identity.mjs";
import { assertTask19OriginBinding, createTask19WebOrigin } from "./task19-web-origin.mjs";

const roots = []; const cleanup = []; afterEach(async () => { await Promise.all(cleanup.splice(0).reverse().map((close) => close())); await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))); });

describe("Task19 protected run-ephemeral TLS identity", () => {
  test("generates unique restrictive identity and deletes every private byte", async () => {
    // Given: two protected run-owned identity roots.
    const firstRoot = await mkdtemp(join(tmpdir(), "task19-tls-first-")); const secondRoot = await mkdtemp(join(tmpdir(), "task19-tls-second-")); roots.push(firstRoot, secondRoot);
    // When: each run creates its own identity.
    const first = await createEphemeralTlsIdentity({ root: join(firstRoot, "identity") }); const second = await createEphemeralTlsIdentity({ root: join(secondRoot, "identity") }); cleanup.push(second.cleanup, first.cleanup);
    // Then: certificate/SPKI bindings differ and private-key permissions are owner-only.
    expect(first.kind).toBe("task19-run-ephemeral-tls"); expect(first.certificateSha256).not.toBe(second.certificateSha256); expect(first.spkiSha256).not.toBe(second.spkiSha256); expect((await stat(first.keyPath)).mode & 0o077).toBe(0); expect(new X509Certificate(first.certificate).keyUsage).toEqual(["1.3.6.1.5.5.7.3.1"]);
    await first.cleanup(); cleanup.pop(); await expect(access(first.keyPath)).rejects.toThrow(); await expect(access(first.certificatePath)).rejects.toThrow();
  });

  test("deletes the identity root when post-generation validation fails", async () => {
    const parent = await mkdtemp(join(tmpdir(), "task19-tls-failure-")); roots.push(parent); const identityRoot = join(parent, "identity");
    await expect(createEphemeralTlsIdentity({ root: identityRoot, platform: "linux", run: async (command) => { await writeFile(command[command.indexOf("-keyout") + 1], "private-key-material"); await writeFile(command[command.indexOf("-out") + 1], "invalid-certificate"); return { exitCode: 0, stdout: "", stderr: "" }; } })).rejects.toThrow();
    await expect(access(identityRoot)).rejects.toThrow();
  });

  test("retries private-material removal after a cleanup failure", async () => {
    const parent = await mkdtemp(join(tmpdir(), "task19-tls-cleanup-retry-"));
    roots.push(parent);
    const identityRoot = join(parent, "identity");
    let attempts = 0;
    const identity = await createEphemeralTlsIdentity({
      root: identityRoot,
      remove: async (...arguments_) => {
        attempts += 1;
        if (attempts === 1) throw new Error("transient cleanup failure");
        await rm(...arguments_);
      },
    });
    await expect(identity.cleanup()).rejects.toThrow("transient cleanup failure");
    await identity.cleanup();
    expect(attempts).toBe(2);
    await expect(access(identityRoot)).rejects.toThrow();
  });

  test("rejects certificate and origin drift from the immutable run binding", () => {
    // Given: the exact generated origin binding recorded for a run.
    const expected = { host: "loopback_dns", port: 9443, certificateSha256: "a".repeat(64), spkiSha256: "b".repeat(64) };
    // When / Then: exact binding passes while cert, host, and port drift fail closed.
    expect(() => assertTask19OriginBinding(expected, expected)).not.toThrow();
    for (const changed of [{ ...expected, certificateSha256: "c".repeat(64) }, { ...expected, host: "loopback_ipv4" }, { ...expected, port: 9444 }]) expect(() => assertTask19OriginBinding(changed, expected)).toThrow("TASK19_WEB_ORIGIN_BINDING_MISMATCH");
  });

  test("rejects repository-known fixture identity at the production origin boundary", async () => {
    // Given: the repository's explicitly test-only certificate and private key.
    const root = await mkdtemp(join(tmpdir(), "task19-known-key-")); roots.push(root); const fixture = { kind: "task19-test-fixture-tls", certificate: await readFile(join(import.meta.dirname, "fixtures/task19-web-origin-cert.pem")), key: await readFile(join(import.meta.dirname, "fixtures/task19-web-origin-key.pem")) };
    // When / Then: production origin creation cannot consume known key bytes.
    await expect(createTask19WebOrigin({ root, identity: fixture })).rejects.toThrow("TASK19_EPHEMERAL_TLS_IDENTITY_REQUIRED");
  });
});
