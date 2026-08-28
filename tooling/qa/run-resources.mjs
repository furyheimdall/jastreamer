import { randomUUID } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";

export class ResourceAllocationError extends Error {
  name = "ResourceAllocationError";
  constructor(code) {
    super(code);
    this.code = code;
  }
}

export const createRunResources = async (baseDirectory) => {
  const runDirectory = await mkdtemp(join(baseDirectory, "run-"));
  return {
    runId: randomUUID(),
    runDirectory,
    deviceIds: {
      server: `server-${randomUUID()}`,
      control: `control-${randomUUID()}`,
      renderer: `renderer-${randomUUID()}`,
      k17: `k17-${randomUUID()}`,
    },
  };
};

export const listenEphemeral = async (server) => {
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolve();
    });
  });
  const address = server.address();
  if (address === null || typeof address === "string") throw new ResourceAllocationError("EPHEMERAL_BIND_FAILED");
  return { host: "127.0.0.1", port: address.port };
};

const closeServer = (server) => new Promise((resolve, reject) => {
  if (!server.listening) { resolve(); return; }
  server.close((error) => error === undefined ? resolve() : reject(error));
});

export const releaseRunResources = async ({ resources, servers = [], processes = [] }) => {
  for (const processHandle of processes) {
    if (processHandle.exitCode === null) {
      const exited = new Promise((resolve) => processHandle.once("exit", resolve));
      processHandle.kill("SIGTERM");
      await exited;
    }
  }
  await Promise.all(servers.map(closeServer));
  await rm(resources.runDirectory, { recursive: true, force: true });
  return {
    resourcesReleased: true,
    processesTerminated: true,
    temporaryDirectoriesRemoved: true,
    externalWrites: 0,
  };
};
