import assert from "node:assert/strict";
import test from "node:test";
import { MediaGrant, NativeHTTPError, NativeServerClient, strictSameOriginURL } from "../lib/native-http.mjs";

const ORIGIN = "https://music.local:8443";

function session(handler) {
  return { fetch: (url, init) => handler(String(url), init) };
}

function stream(chunks) {
  return new ReadableStream({
    pull(controller) {
      const next = chunks.shift();
      if (next === undefined) controller.close();
      else controller.enqueue(Uint8Array.from(next));
    },
  });
}

test("authenticated API mutations stay same-origin and carry browser CSRF and owner authority", async () => {
  let seen;
  const client = new NativeServerClient(session(async (url, init) => {
    seen = { url, init };
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  }), ORIGIN);

  await client.json("POST", "/api/v1/browser-output/registrations/r/reports", {
    ownerToken: "owner-secret",
    body: { sequence: 7, result: "succeeded" },
  });

  assert.equal(seen.url, `${ORIGIN}/api/v1/browser-output/registrations/r/reports`);
  assert.equal(seen.init.credentials, "include");
  assert.equal(seen.init.redirect, "error");
  assert.equal(seen.init.headers.get("Origin"), ORIGIN);
  assert.equal(seen.init.headers.get("X-Jastreamer-Request"), "web");
  assert.equal(seen.init.headers.get("X-Jastreamer-Browser-Token"), "owner-secret");
});

test("redirected authenticated requests are denied before response data is consumed", async () => {
  let cancelled = false;
  const client = new NativeServerClient(session(async () => ({
    ok: true,
    status: 200,
    redirected: true,
    url: "https://other.local/api/v1/player",
    body: { cancel: async () => { cancelled = true; } },
  })), ORIGIN);
  await assert.rejects(
    client.json("GET", "/api/v1/player"),
    (error) => error instanceof NativeHTTPError && error.serverCode === "REDIRECT_DENIED",
  );
  assert.equal(cancelled, true);
});

test("media grants reject arbitrary origins, credentials, non-media paths, queries and fragments", () => {
  for (const value of [
    "https://other.local/media/grant/file.flac",
    "https://user:secret@music.local:8443/media/grant/file.flac",
    "/api/v1/player",
    "/media/grant/file.flac?token=secret",
    "/media/grant/file.flac#secret",
    "//other.local/media/grant/file.flac",
    "/media/%2e%2e/api/v1/player",
    "/media/grant%5c..%5capi",
  ]) {
    assert.equal(strictSameOriginURL(ORIGIN, value, { media: true }), null, value);
  }
  assert.equal(strictSameOriginURL(ORIGIN, "/media/grant/file.flac", { media: true })?.href, `${ORIGIN}/media/grant/file.flac`);
});

test("seekable media broker accepts only an exact bounded 206 response", async () => {
  const requests = [];
  const client = new NativeServerClient(session(async (url, init) => {
    requests.push({ url, init });
    return new Response(Uint8Array.from([1, 2, 3, 4]), {
      status: 206,
      headers: { "content-range": "bytes 4-7/8" },
    });
  }), ORIGIN);
  const grant = new MediaGrant(client, { playID: "p1", url: "/media/g/file.flac", size: 8, seekable: true });
  const result = await grant.read(4, 4);
  assert.deepEqual([...result.data], [1, 2, 3, 4]);
  assert.equal(result.eof, true);
  assert.equal(result.size, 8);
  assert.equal(requests[0].init.headers.get("Range"), "bytes=4-7");
  assert.equal(requests[0].init.credentials, "include");
});

test("seekable media broker rejects ignored, mismatched and oversized ranges", async () => {
  const responses = [
    new Response(Uint8Array.from([1]), { status: 200 }),
    new Response(Uint8Array.from([1]), { status: 206, headers: { "content-range": "bytes 2-2/8" } }),
    new Response(Uint8Array.from([1, 2]), { status: 206, headers: { "content-range": "bytes 0-0/8" } }),
  ];
  const client = new NativeServerClient(session(async () => responses.shift()), ORIGIN);
  for (const expected of ["INVALID_RANGE_RESPONSE", "INVALID_RANGE_RESPONSE", "RESPONSE_TOO_LARGE"]) {
    const grant = new MediaGrant(client, { playID: "p", url: "/media/g/file.flac", size: 8, seekable: true });
    await assert.rejects(grant.read(0, 1), (error) => error instanceof NativeHTTPError && error.serverCode === expected);
  }
});

test("nonseekable transformed source uses one no-Range stream and serves bounded sequential reads", async () => {
  const requests = [];
  const client = new NativeServerClient(session(async (_url, init) => {
    requests.push(init);
    return new Response(stream([[1, 2, 3], [4, 5, 6]]), { status: 200 });
  }), ORIGIN);
  const grant = new MediaGrant(client, { playID: "p", url: "/media/g/file.wav", size: -1, seekable: false });

  assert.deepEqual([...((await grant.read(0, 2)).data)], [1, 2]);
  assert.deepEqual([...((await grant.read(2, 3)).data)], [3, 4, 5]);
  const end = await grant.read(5, 3);
  assert.deepEqual([...end.data], [6]);
  assert.equal(end.eof, true);
  assert.equal(end.size, 6);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].headers.has("Range"), false);
  await assert.rejects(grant.read(1, 1), (error) => error.serverCode === "NONSEEKABLE_RANGE");
});

test("cancelling a media grant wakes a blocked sequential reader and forbids stale ownership reads", async () => {
  let cancelled = false;
  const body = new ReadableStream({
    pull() { return new Promise(() => {}); },
    cancel() { cancelled = true; },
  });
  const client = new NativeServerClient(session(async () => new Response(body, { status: 200 })), ORIGIN);
  const grant = new MediaGrant(client, { playID: "old-play", url: "/media/g/file.wav", seekable: false });
  const reading = grant.read(0, 64);
  await new Promise((resolve) => setImmediate(resolve));
  grant.cancel();
  await assert.rejects(reading, (error) => error.name === "AbortError" || error.serverCode === "MEDIA_CANCELLED");
  await assert.rejects(grant.read(0, 1), (error) => error.serverCode === "MEDIA_CANCELLED");
  assert.equal(cancelled, true);
});
