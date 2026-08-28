import { expect, test } from "bun:test";
import { createServer } from "node:http";
import { createConnection } from "node:net";
import { createServer as createTcpServer } from "node:net";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createRunResources, listenEphemeral, releaseRunResources } from "./run-resources.mjs";
import { awaitSubscribedMutation } from "./subscribe-before-mutate.mjs";
import { generateMediaFixtures } from "./generate-media-fixtures.mjs";
import { trackServerConnections } from "./control-servers.mjs";

const ffmpegPath = "/usr/bin/ffmpeg";

test("generates byte-identical five-codec 1 kHz and seek fixtures", async () => {
  // Given
  const first = await mkdtemp(join(tmpdir(), "jastreamer-media-first-"));
  const second = await mkdtemp(join(tmpdir(), "jastreamer-media-second-"));
  try {
    // When
    const [left, right] = await Promise.all([
      generateMediaFixtures({ outputDirectory: first, ffmpegPath }),
      generateMediaFixtures({ outputDirectory: second, ffmpegPath }),
    ]);

    // Then
    expect(left).toEqual(right);
    expect(left.files).toHaveLength(10);
    expect(new Set(left.files.map((file) => file.codec))).toEqual(new Set(["flac", "mp3", "ogg", "opus", "wav"]));
    expect(new Set(left.files.map((file) => file.signal))).toEqual(new Set(["tone_1khz", "seek_tones"]));
    for (const file of left.files) {
      expect(await readFile(join(first, file.relativePath))).toEqual(await readFile(join(second, file.relativePath)));
    }
  } finally {
    await Promise.all([rm(first, { recursive: true, force: true }), rm(second, { recursive: true, force: true })]);
  }
});

test("allocates disjoint resources to concurrent fixture runs and cleans both", async () => {
  // Given
  const base = await mkdtemp(join(tmpdir(), "jastreamer-runs-"));
  const servers = [createServer(), createServer()];
  try {
    // When
    const [left, right] = await Promise.all([createRunResources(base), createRunResources(base)]);
    const [leftEndpoint, rightEndpoint] = await Promise.all(servers.map((server) => listenEphemeral(server)));

    // Then
    expect(left.runDirectory).not.toBe(right.runDirectory);
    expect(left.deviceIds).not.toEqual(right.deviceIds);
    expect(leftEndpoint.port).not.toBe(rightEndpoint.port);
    expect(await releaseRunResources({ resources: left, servers: [servers[0]] })).toEqual({
      resourcesReleased: true, processesTerminated: true, temporaryDirectoriesRemoved: true, externalWrites: 0,
    });
    expect(await releaseRunResources({ resources: right, servers: [servers[1]] })).toEqual({
      resourcesReleased: true, processesTerminated: true, temporaryDirectoriesRemoved: true, externalWrites: 0,
    });
  } finally {
    for (const server of servers) server.closeAllConnections();
    await rm(base, { recursive: true, force: true });
  }
});

test("subscribes before triggering a mutation", async () => {
  // Given
  const order = [];
  let notify;
  const subscribe = () => {
    order.push("subscribe");
    return {
      signal: new Promise((resolve) => { notify = resolve; }),
      unsubscribe: () => order.push("unsubscribe"),
    };
  };
  const mutate = async () => {
    order.push("mutate");
    notify({ sequence: 7 });
  };

  // When
  const event = await awaitSubscribedMutation({ subscribe, mutate, timeoutMs: 1_000 });

  // Then
  expect(event).toEqual({ sequence: 7 });
  expect(order).toEqual(["subscribe", "mutate", "unsubscribe"]);
});

test("tracked Server teardown closes upgraded or otherwise persistent sockets", async () => {
  // Given
  let connections;
  let accept;
  const accepted = new Promise((resolve) => { accept = resolve; });
  const server = createTcpServer((serverSocket) => {
    connections.track(serverSocket);
    accept();
  });
  connections = trackServerConnections(server);
  const endpoint = await listenEphemeral(server);
  const socket = createConnection({ host: endpoint.host, port: endpoint.port });
  const connected = new Promise((resolve, reject) => {
    socket.once("connect", resolve);
    socket.once("error", reject);
  });
  await Promise.all([connected, accepted]);
  // When
  const result = await connections.close();

  // Then
  expect(result).toEqual({ destroyedSockets: 1 });
  expect(server.listening).toBe(false);
  socket.destroy();
});
