import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { NativeControllerError, NativeDesktopController } from "../lib/native-controller.mjs";

const SERVER = {
  id: "11111111-1111-4111-8111-111111111111",
  origin: "https://music.local:8443",
};

class MemoryPreferences {
  value = { enabled: true, device_id: "default", exclusive: false, volume: 1 };
  async load() { return { ...this.value }; }
  async set(changes) { Object.assign(this.value, changes); return { ...this.value }; }
}

class FakeHelper extends EventEmitter {
  running = true;
  requests = [];
  statusState = "stopped";
  async request(method, params) {
    this.requests.push({ method, params });
    if (method === "devices") return { available: true, devices: [{ id: "default", name: "Default", is_default: true }] };
    if (method === "status" || method === "set_volume") return audio(this.statusState);
    if (method === "stop") return { observation: observation("stopped", params.play_id), audio: audio("stopped") };
    return {};
  }
  notify() { return true; }
  async shutdown() { this.running = false; }
}

function audio(state) {
  return { state, play_id: "", sequence: 0, volume: 1, actual: null };
}
function observation(event, playID) {
  return { event, play_id: playID, position_ms: 0, duration_ms: 0, has_position: false };
}
function json(value, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
}
function registration() {
  return {
    registration_id: "registration-1",
    owner_token: "owner-secret",
    lease_duration_ms: 15_000,
    poll_after_ms: 1_000,
    device: {
      id: "browser:windows", name: "Windows output", protocol: "browser", online: true,
      capabilities: { play: true, pause: true, stop: true, seek: true }, protocol_info: [],
    },
  };
}
async function controller(targetSession, helper = new FakeHelper(), platform = "win32") {
  const value = new NativeDesktopController({
    platform,
    preferences: new MemoryPreferences(),
    helper,
    probe: async () => SERVER,
  });
  await value.initialize();
  return { value, helper, session: targetSession };
}

async function waitFor(predicate, timeout = 2_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const result = predicate();
    if (result) return result;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error("condition timed out");
}

test("connect_if_available never creates a registration", async () => {
  const requests = [];
  const targetSession = { fetch: async (...args) => { requests.push(args); throw new Error("unexpected request"); } };
  const { value } = await controller(targetSession);
  const state = await value.request(SERVER, targetSession, "connect_if_available", { name: "Windows output" });
  assert.equal(state.device, null);
  assert.equal(requests.length, 0);
  await value.shutdown();
});

test("document cancellation cannot claim a late registration and releases its owner authority", async () => {
  let releaseResponse;
  const registrationResponse = new Promise((resolve) => { releaseResponse = resolve; });
  const requests = [];
  const targetSession = { fetch: async (url, init) => {
    requests.push({ url: String(url), method: init.method, owner: init.headers.get("X-Jastreamer-Browser-Token") });
    if (init.method === "POST") return registrationResponse;
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    throw new Error("unexpected request");
  } };
  const { value } = await controller(targetSession);
  const abort = new AbortController();
  const connecting = value.request(SERVER, targetSession, "connect", { name: "Windows output" }, { signal: abort.signal });
  await waitFor(() => requests.some((request) => request.method === "POST"));
  abort.abort();
  releaseResponse(json(registration()));
  await assert.rejects(connecting, (error) => error.name === "AbortError");
  await waitFor(() => requests.some((request) => request.method === "DELETE"));
  const deletion = requests.find((request) => request.method === "DELETE");
  assert.equal(deletion.owner, "owner-secret");
  assert.equal(value.state().device, null);
  await value.shutdown();
});

test("cancel_before_sequence fences a stale command before it reaches the helper", async () => {
  let polls = 0;
  const targetSession = { fetch: async (_url, init) => {
    if (init.method === "POST") return json(registration());
    if (init.method === "GET") {
      polls++;
      return json({
        lease_duration_ms: 15_000,
        poll_after_ms: 1_000,
        cancel_before_sequence: 8,
        command: {
          sequence: 8,
          action: "set_uri",
          play_id: "stale-play",
          resource: { url: "/media/stale/file.flac", mime: "audio/flac", seekable: true, size: 4 },
        },
      });
    }
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return json({ lease_duration_ms: 15_000 });
  } };
  const { value, helper } = await controller(targetSession);
  await value.request(SERVER, targetSession, "connect", { name: "Windows output" });
  await waitFor(() => polls > 0);
  assert.equal(helper.requests.some((request) => request.method === "set_uri"), false);
  assert.equal(value.state().device.id, "browser:windows");
  await value.shutdown();
});

test("invalid lease terminates ownership instead of silently extending it", async () => {
  const targetSession = { fetch: async (_url, init) => {
    if (init.method === "POST") return json(registration());
    if (init.method === "GET") return json({ lease_duration_ms: 0, poll_after_ms: 100 });
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return json({ lease_duration_ms: 15_000 });
  } };
  const { value } = await controller(targetSession);
  await value.request(SERVER, targetSession, "connect", { name: "Windows output" });
  const state = await waitFor(() => value.state().error?.code === "invalid_lease" ? value.state() : null);
  assert.equal(state.device, null);
  assert.equal(state.audio.state, "stopped");
  await value.shutdown();
});

test("authentication loss tears down local ownership and is not reported as a successful stop", async () => {
  const targetSession = { fetch: async (_url, init) => {
    if (init.method === "POST") return json(registration());
    if (init.method === "GET") return json({ error: { code: "AUTHENTICATION_REQUIRED" } }, 401);
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return json({ lease_duration_ms: 15_000 });
  } };
  const { value } = await controller(targetSession);
  await value.request(SERVER, targetSession, "connect", { name: "Windows output" });
  const state = await waitFor(() => value.state().error?.code === "authentication_required" ? value.state() : null);
  assert.equal(state.device, null);
  assert.equal(state.error.code, "authentication_required");
  await value.shutdown();
});

test("a registration unknown to the Server is reported as a lost connection, not as sign-in, and is not replayed to a new document", async () => {
  const targetSession = { fetch: async (_url, init) => {
    if (init.method === "POST") return json(registration());
    if (init.method === "GET") return json({ error: { code: "BROWSER_OUTPUT_NOT_FOUND" } }, 404);
    if (init.method === "DELETE") return json({ error: { code: "BROWSER_OUTPUT_NOT_FOUND" } }, 404);
    return json({ error: { code: "BROWSER_OUTPUT_NOT_FOUND" } }, 404);
  } };
  const { value } = await controller(targetSession);
  await value.request(SERVER, targetSession, "connect", { name: "Windows output" });
  const state = await waitFor(() => value.state().error ? value.state() : null);
  assert.equal(state.device, null);
  assert.equal(state.error.code, "registration_lost");
  await value.prepareRemoteServer(SERVER);
  assert.equal(value.state().error, undefined, "A new Server document must not receive the ended connection's error");
  await value.shutdown();
});

test("configuration keeps the output across endpoint/mode changes and replaces it only when leaving native audio", async () => {
  const requests = [];
  const targetSession = { fetch: async (url, init) => {
    requests.push({ url: String(url), method: init.method });
    if (init.method === "POST") return json(registration());
    if (init.method === "GET") return json({ lease_duration_ms: 15_000, poll_after_ms: 1_000 });
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return json({ lease_duration_ms: 15_000 });
  } };
  const helper = new FakeHelper();
  const { value } = await controller(targetSession, helper);
  await value.request(SERVER, targetSession, "connect", { name: "Windows output" });
  helper.statusState = "playing";
  await assert.rejects(
    value.request(SERVER, targetSession, "configure", { enabled: true, device_id: "default", exclusive: true }),
    (error) => error instanceof NativeControllerError && error.code === "stop_required",
  );
  await assert.rejects(
    value.prepareRemoteServer({ ...SERVER, id: "22222222-2222-4222-8222-222222222222" }),
    (error) => error.code === "stop_required",
  );
  assert.equal(value.state().device.id, "browser:windows");
  assert.equal(requests.some((request) => request.method === "DELETE"), false);

  helper.statusState = "stopped";
  const configured = await value.request(
    SERVER,
    targetSession,
    "configure",
    { enabled: true, device_id: "default", exclusive: true },
  );
  assert.equal(configured.device.id, "browser:windows", "An exclusive-mode change must keep the selected output");
  assert.equal(configured.audio.requested.exclusive, true);
  assert.equal(configured.volume, null, "Exclusive mode must not offer app-local volume");
  await assert.rejects(
    value.request(SERVER, targetSession, "set_volume", { volume: 0.5 }),
    (error) => error.code === "exclusive_fixed_volume",
  );
  assert.equal(requests.some((request) => request.method === "DELETE"), false);
  const browser = await value.request(SERVER, targetSession, "configure", { enabled: false, device_id: "default", exclusive: false });
  assert.equal(browser.device, null);
  assert.equal(requests.filter((request) => request.method === "DELETE").length, 1);
  await value.shutdown();
});

for (const platform of ["win32", "darwin"]) test(`${platform} media controls failure never becomes a media failure or Stop`, async () => {
  const targetSession = { fetch: async (_url, init) => {
    if (init.method === "POST") return json(registration());
    if (init.method === "GET") return json({ lease_duration_ms: 15_000, poll_after_ms: 1_000 });
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return json({ lease_duration_ms: 15_000 });
  } };
  const { value, helper } = await controller(targetSession, undefined, platform);
  await value.request(SERVER, targetSession, "connect", { name: "Windows output" });
  helper.emit("notification", { event: "state", audio: { ...audio("playing"), play_id: "play-1", sequence: 1 } });
  const before = helper.requests.filter((request) => request.method === "stop").length;
  helper.emit("notification", {
    event: "media_controls_error",
    error: { code: "media_controls_unavailable", message: "System media controls could not be updated." },
  });
  const state = value.state();
  assert.equal(state.error.code, "media_controls_unavailable");
  assert.equal(state.audio.state, "playing");
  assert.equal(state.device.id, "browser:windows");
  assert.equal(helper.requests.filter((request) => request.method === "stop").length, before);
  helper.statusState = "stopped";
  await value.request(SERVER, targetSession, "disconnect", {});
  await value.shutdown();
});

test("unsupported actions fail without issuing Server or helper commands", async () => {
  const targetSession = { fetch: async () => { throw new Error("unexpected request"); } };
  const { value, helper } = await controller(targetSession);
  const before = helper.requests.length;
  await assert.rejects(
    value.request(SERVER, targetSession, "launch_url", { url: "file:///secret" }),
    (error) => error.code === "invalid_request",
  );
  assert.equal(helper.requests.length, before);
  await value.shutdown();
});

class PlaybackHelper extends FakeHelper {
  current = null;
  mediaControls = null;
  async request(method, params = {}) {
    if (method === "media_controls") {
      this.requests.push({ method, params });
      this.mediaControls = params;
      return {};
    }
    if (!["set_uri", "play", "pause", "stop", "seek"].includes(method)) return super.request(method, params);
    this.requests.push({ method, params });
    if (method !== "set_uri" && !this.current) {
      throw Object.assign(new Error("No media is loaded"), { code: "action_failed" });
    }
    if (method === "set_uri") this.current = { play_id: params.play_id, sequence: params.sequence };
    this.current.sequence = params.sequence;
    this.statusState = method === "play" ? "playing" : method === "stop" ? "stopped" : "paused";
    const event = { set_uri: "loaded", play: "playing", pause: "pause", stop: "stopped", seek: "seeked" }[method];
    return {
      observation: { ...observation(event, params.play_id), state: this.statusState === "playing" ? "playing" : "paused" },
      audio: { ...audio(method === "set_uri" ? "loaded" : this.statusState), play_id: params.play_id, sequence: params.sequence },
    };
  }
  finish() {
    const current = this.current;
    this.current = null;
    this.statusState = "stopped";
    this.emit("notification", {
      event: "observation", ...current,
      observation: { ...observation("ended", current.play_id), position_ms: 1_000, duration_ms: 1_000, has_position: true },
      audio: { ...audio("stopped"), ...current },
    });
  }
}

async function playbackFixture(t, onReport = () => undefined, platform = "win32", onRequest = () => undefined) {
  const commands = [];
  const reports = [];
  const requests = [];
  let polls = 0;
  let cancelBefore = 0;
  const targetSession = { fetch: async (url, init) => {
    requests.push({ url: String(url), method: init.method, body: init.body && JSON.parse(init.body), credentials: init.credentials });
    const pathname = new URL(url).pathname;
    const response = await onRequest(url, init);
    if (response) return response;
    if (pathname === "/api/v1/player") return init.method === "GET"
      ? json({ renderer_id: "browser:windows" }) : json({});
    if (pathname.endsWith("/registrations") && init.method === "POST") return json(registration());
    if (pathname.endsWith("/commands")) {
      polls++;
      return json({ lease_duration_ms: 15_000, poll_after_ms: 100, cancel_before_sequence: cancelBefore, command: commands.shift() });
    }
    if (pathname.endsWith("/reports")) {
      const report = JSON.parse(init.body);
      reports.push(report);
      return await onReport(report) ?? new Response(null, { status: 204 });
    }
    if (init.method === "DELETE") return new Response(null, { status: 204 });
    return json({ lease_duration_ms: 15_000, cancel_before_sequence: cancelBefore });
  } };
  const helper = new PlaybackHelper();
  const { value } = await controller(targetSession, helper, platform);
  t.after(() => value.shutdown());
  const load = (playID, sequence, resource = {}) => commands.push({
    action: "set_uri", play_id: playID, sequence,
    resource: { url: `/media/${playID}/fixture.wav`, mime: "audio/wav", seekable: true, size: 128, duration_ms: 1_000, ...resource },
  }, { action: "play", play_id: playID, sequence: sequence + 1 });
  await value.request(SERVER, targetSession, "connect", { name: "Windows output" });
  return { value, helper, commands, reports, requests, load, polls: () => polls, cancel: (sequence) => { cancelBefore = sequence; } };
}

for (const platform of ["win32", "darwin"]) test(`${platform} completion during Play acknowledgement withdraws controls immediately and reports after the command`, async (t) => {
  let acknowledge;
  const pending = new Promise((resolve) => { acknowledge = resolve; });
  t.after(() => acknowledge(new Response(null, { status: 204 })));
  const fixture = await playbackFixture(t, (report) => report.sequence === 2 && report.result ? pending : undefined, platform);
  fixture.load("short", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.emit("notification", {
    event: "observation", play_id: "short", sequence: 2,
    observation: { ...observation("ended", "short"), position_ms: 1_000, duration_ms: 1_000, has_position: true },
    audio: { ...audio("stopped"), play_id: "short", sequence: 2 },
  });
  assert.equal(fixture.helper.mediaControls.enabled, false);
  acknowledge(new Response(null, { status: 204 }));
  await waitFor(() => fixture.reports.some((report) => report.observation?.event === "ended"));
  assert.equal(fixture.reports.filter((report) => report.observation?.event === "ended").length, 1);
  assert.equal(fixture.value.state().audio.state, "stopped");
  assert.equal(fixture.helper.mediaControls.enabled, false);
});

test("a delayed stale observation cannot cancel the following track", async (t) => {
  let releaseOld;
  const oldResponse = new Promise((resolve) => { releaseOld = resolve; });
  t.after(() => releaseOld(new Response(null, { status: 204 })));
  const fixture = await playbackFixture(t, (report) =>
    report.observation?.event === "timeupdate" && report.observation.play_id === "A" ? oldResponse : undefined);
  fixture.load("A", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  await new Promise((resolve) => setTimeout(resolve, 1_000));
  fixture.helper.emit("notification", {
    event: "observation", play_id: "A", sequence: 2,
    observation: { ...observation("timeupdate", "A"), state: "playing", position_ms: 500, duration_ms: 1_000, has_position: true },
    audio: { ...audio("playing"), play_id: "A", sequence: 2 },
  });
  await waitFor(() => fixture.reports.some((report) => report.observation?.event === "timeupdate"));
  fixture.commands.push({ action: "stop", play_id: "A", sequence: 3 });
  fixture.load("B", 4);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 5 && report.result === "succeeded"));
  const before = fixture.polls();
  releaseOld(json({ error: { code: "STALE_BROWSER_REPORT" } }, 409));
  await waitFor(() => fixture.polls() > before);
  assert.equal(fixture.value.state().audio.state, "playing");
  assert.equal(fixture.helper.current.play_id, "B");
  assert.equal(fixture.value.state().device.id, "browser:windows");
});

test("confirmed helper exit permits browser recovery without restarting native audio", async (t) => {
  const fixture = await playbackFixture(t);
  fixture.load("crash", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.running = false;
  fixture.helper.emit("terminal", new Error("helper exited"));
  await waitFor(() => fixture.value.state().error?.code === "helper_terminated");
  assert.equal(fixture.value.state().audio.can_configure, true);
  const state = await fixture.value.request(SERVER, {}, "configure", {
    enabled: false, device_id: "default", exclusive: false,
  });
  assert.equal(state.audio.enabled, false);
  assert.equal(state.device, null);
});

test("macOS document reload retains Server-owned playback and Quit releases only its local transport", async (t) => {
  const fixture = await playbackFixture(t, undefined, "darwin");
  fixture.load("mac-reload", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  const registeredDevice = fixture.value.state().device.id;
  const beforeStops = fixture.helper.requests.filter((request) => request.method === "stop").length;
  await fixture.value.prepareRemoteServer({ ...SERVER });
  const resumed = await fixture.value.request(SERVER, {}, "connect_if_available", { name: "macOS output" });
  assert.equal(resumed.device.id, registeredDevice);
  assert.equal(resumed.audio.platform, "macos");
  assert.equal(resumed.audio.state, "playing");
  assert.equal(fixture.helper.requests.filter((request) => request.method === "stop").length, beforeStops);
  assert.equal(fixture.requests.filter((request) => request.method === "POST" && request.url.endsWith("/registrations")).length, 1);
  assert.equal(fixture.helper.mediaControls.enabled, true);
  assert.equal(fixture.helper.mediaControls.state, "playing");
  await assert.rejects(
    fixture.value.prepareRemoteServer({ ...SERVER, origin: "https://another.local" }),
    (error) => error.code === "stop_required",
  );
  await fixture.value.shutdown();
  assert.equal(fixture.helper.running, false);
  assert.equal(fixture.helper.requests.filter((request) => request.method === "stop").length, beforeStops + 1);
  assert.equal(fixture.requests.filter((request) => request.method === "DELETE").length, 1);
  assert.equal(fixture.requests.some((request) => new URL(request.url).pathname === "/api/v1/player"), false,
    "Quit must never send Stop to the Server's selected network receiver");
  assert.equal(fixture.helper.mediaControls.enabled, false);
});

test("macOS terminal helper loss does not auto-register or replay after a document reload", async (t) => {
  const fixture = await playbackFixture(t, undefined, "darwin");
  fixture.load("mac-terminal", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.running = false;
  fixture.helper.emit("terminal", new Error("DAC helper exited"));
  await waitFor(() => fixture.value.state().error?.code === "helper_terminated");
  await fixture.value.prepareRemoteServer({ ...SERVER });
  const afterLoss = await fixture.value.request(SERVER, {}, "connect_if_available", { name: "macOS output" });
  assert.equal(afterLoss.device, null);
  assert.notEqual(afterLoss.audio.state, "playing");
  assert.equal(fixture.requests.filter((request) => request.method === "POST" && request.url.endsWith("/registrations")).length, 1);
  assert.equal(fixture.helper.requests.filter((request) => request.method === "set_uri").length, 1);
  const before = fixture.requests.length;
  fixture.helper.emit("notification", { event: "transport", action: "play" });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(fixture.requests.length, before, "A remote command must not recreate lost registration");
});

test("Server retirement after EOF preserves registration while a terminal report acknowledgement is pending", async (t) => {
  let acknowledge;
  const pending = new Promise((resolve) => { acknowledge = resolve; });
  t.after(() => acknowledge(new Response(null, { status: 204 })));
  const fixture = await playbackFixture(t, (report) => report.observation?.event === "ended" ? pending : undefined, "darwin");
  fixture.load("completed", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.finish();
  await waitFor(() => fixture.reports.some((report) => report.observation?.event === "ended"));
  const polls = fixture.polls();
  fixture.cancel(2);
  await waitFor(() => fixture.polls() > polls + 1);
  assert.equal(fixture.value.state().audio.state, "stopped");
  assert.equal(fixture.value.state().error, undefined);
  assert.equal(fixture.value.state().device.id, "browser:windows");
  assert.equal(fixture.helper.running, true);
  assert.equal(fixture.helper.requests.filter((request) => request.method === "stop").length, 0);
  assert.equal(fixture.helper.mediaControls.enabled, false);
  acknowledge(new Response(null, { status: 204 }));
  fixture.load("following", 3);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 4 && report.result === "succeeded"));
  assert.equal(fixture.value.state().audio.state, "playing");
  assert.equal(fixture.helper.current.play_id, "following");
  assert.equal(fixture.helper.mediaControls.enabled, true);
  assert.equal(fixture.requests.filter((request) => request.method === "POST" && request.url.endsWith("/registrations")).length, 1);
});

test("Quit after EOF does not Stop a helper transport lease already released by its terminal notification", async (t) => {
  const fixture = await playbackFixture(t, undefined, "darwin");
  fixture.load("finished-before-quit", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.finish();
  await waitFor(() => fixture.reports.some((report) => report.observation?.event === "ended"));
  await fixture.value.shutdown();
  assert.equal(fixture.helper.requests.filter((request) => request.method === "stop").length, 0);
  assert.equal(fixture.helper.running, false);
  assert.equal(fixture.helper.mediaControls.enabled, false);
  assert.equal(fixture.value.state().error, undefined);
  assert.equal(fixture.requests.filter((request) => request.method === "DELETE").length, 1);
});

test("macOS Now Playing follows accepted media metadata, artwork, timeline and replacement without autoplay", async (t) => {
  const artwork = Buffer.from("bounded artwork");
  const fixture = await playbackFixture(t, undefined, "darwin", (url) =>
    new URL(url).pathname === "/artwork/first" ? new Response(artwork) : undefined);
  fixture.commands.push({
    action: "set_uri", play_id: "first", sequence: 1,
    resource: {
      url: "/media/first/fixture.wav", mime: "audio/wav", seekable: true, size: 128,
      title: "First", artist: "Artist", album: "Album", duration_ms: 10_000, artwork_url: "/artwork/first",
    },
  });
  await waitFor(() => fixture.reports.some((report) => report.sequence === 1 && report.result === "succeeded"));
  assert.equal(fixture.value.state().audio.state, "loaded");
  assert.equal(fixture.helper.requests.some((request) => request.method === "play"), false);
  assert.deepEqual(fixture.helper.mediaControls, {
    enabled: true, state: "paused", position_ms: 0, duration_ms: 10_000, seekable: true,
    title: "First", artist: "Artist", album: "Album", artwork_base64: artwork.toString("base64"),
  });
  const artworkRequest = fixture.requests.find((request) => new URL(request.url).pathname === "/artwork/first");
  assert.equal(artworkRequest.credentials, "include");
  fixture.commands.push({ action: "play", play_id: "first", sequence: 2 });
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.emit("notification", {
    event: "observation", play_id: "first", sequence: 2,
    observation: { ...observation("timeupdate", "first"), state: "playing", position_ms: 4_000, duration_ms: 10_000, has_position: true },
    audio: { ...audio("playing"), play_id: "first", sequence: 2 },
  });
  assert.equal(fixture.helper.mediaControls.position_ms, 4_000);
  assert.equal(fixture.helper.mediaControls.state, "playing");
  fixture.load("second", 3, { title: "Second", seekable: false, artwork_url: "https://untrusted.example/art.jpg" });
  await waitFor(() => fixture.reports.some((report) => report.sequence === 4 && report.result === "succeeded"));
  assert.equal(fixture.helper.mediaControls.title, "Second");
  assert.equal(fixture.helper.mediaControls.artist, "");
  assert.equal(fixture.helper.mediaControls.seekable, false);
  assert.equal(fixture.helper.mediaControls.artwork_base64, undefined);
  assert.equal(fixture.requests.some((request) => new URL(request.url).hostname === "untrusted.example"), false);
  fixture.helper.finish();
  assert.equal(fixture.helper.mediaControls.enabled, false);
  assert.equal(fixture.helper.mediaControls.title, "");
});

test("macOS remote media actions require authenticated Server authority and never directly alter local playback", async (t) => {
  const fixture = await playbackFixture(t, undefined, "darwin");
  fixture.load("remote", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  const localCommands = fixture.helper.requests.filter((request) => ["play", "pause", "stop", "seek"].includes(request.method));
  for (const command of [{ action: "pause" }, { action: "seek", position_ms: 500 }, { action: "next" }]) {
    fixture.helper.emit("notification", { event: "transport", ...command });
  }
  await waitFor(() => fixture.requests.filter((request) => request.url.endsWith("/player") && request.method === "POST").length === 3);
  const controls = fixture.requests.filter((request) => request.url.endsWith("/player"));
  assert.deepEqual(controls.map((request) => request.method), ["GET", "POST", "GET", "POST", "GET", "POST"]);
  assert.deepEqual(controls.filter((request) => request.method === "POST").map((request) => request.body), [
    { action: "pause" }, { action: "seek", position_ms: 500 }, { action: "next" },
  ]);
  assert.equal(controls.every((request) => request.credentials === "include"), true);
  assert.deepEqual(fixture.helper.requests.filter((request) => ["play", "pause", "stop", "seek"].includes(request.method)), localCommands);
  assert.equal(fixture.value.state().audio.state, "playing");
  fixture.load("stream", 3, { seekable: false });
  await waitFor(() => fixture.reports.some((report) => report.sequence === 4 && report.result === "succeeded"));
  const before = fixture.requests.filter((request) => request.url.endsWith("/player")).length;
  fixture.helper.emit("notification", { event: "transport", action: "seek", position_ms: 100 });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(fixture.requests.filter((request) => request.url.endsWith("/player")).length, before);
});

test("macOS ownership mismatch withdraws controls without stopping audio or letting later timeline updates reclaim them", async (t) => {
  const fixture = await playbackFixture(t, undefined, "darwin", (url) =>
    new URL(url).pathname === "/api/v1/player" ? json({ renderer_id: "network:receiver" }) : undefined);
  fixture.load("ownership", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.emit("notification", { event: "transport", action: "pause" });
  await waitFor(() => fixture.helper.mediaControls.enabled === false);
  fixture.helper.emit("notification", {
    event: "observation", play_id: "ownership", sequence: 2,
    observation: { ...observation("timeupdate", "ownership"), state: "playing", position_ms: 100, has_position: true },
    audio: { ...audio("playing"), play_id: "ownership", sequence: 2 },
  });
  fixture.helper.emit("notification", { event: "transport", action: "next" });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(fixture.helper.mediaControls.enabled, false);
  assert.equal(fixture.value.state().audio.state, "playing");
  assert.equal(fixture.helper.requests.some((request) => request.method === "stop"), false);
  assert.equal(fixture.requests.some((request) => request.url.endsWith("/player") && request.method === "POST"), false);
  assert.equal(fixture.requests.filter((request) => request.url.endsWith("/player")).length, 1);
});

test("macOS late ownership response after disconnect cannot forward a control or resurrect registration", async (t) => {
  let release;
  const pending = new Promise((resolve) => { release = resolve; });
  t.after(() => release(json({ renderer_id: "browser:windows" })));
  const fixture = await playbackFixture(t, undefined, "darwin", (url) =>
    new URL(url).pathname === "/api/v1/player" ? pending : undefined);
  fixture.load("disconnect", 1);
  await waitFor(() => fixture.reports.some((report) => report.sequence === 2 && report.result === "succeeded"));
  fixture.helper.emit("notification", { event: "transport", action: "play" });
  await waitFor(() => fixture.requests.some((request) => request.url.endsWith("/player")));
  await fixture.value.request(SERVER, {}, "disconnect");
  release(json({ renderer_id: "browser:windows" }));
  await new Promise((resolve) => setImmediate(resolve));
  await fixture.value.prepareRemoteServer(SERVER);
  const state = await fixture.value.request(SERVER, {}, "connect_if_available", { name: "macOS output" });
  assert.equal(state.device, null);
  assert.equal(fixture.helper.mediaControls.enabled, false);
  assert.equal(fixture.requests.some((request) => request.url.endsWith("/player") && request.method === "POST"), false);
  assert.equal(fixture.requests.filter((request) => request.method === "POST" && request.url.endsWith("/registrations")).length, 1);
});
