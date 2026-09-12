import assert from "node:assert/strict";
import test from "node:test";
import { createServer } from "node:http";
import { once } from "node:events";
import { ProbeError, probeEndpoint } from "../lib/probe.mjs";

const SERVER_ID = "11111111-1111-4111-8111-111111111111";

function discoveryResponse(overrides = {}, init = {}) {
  return new Response(
    JSON.stringify({
      product: "jastreamer",
      protocol: 1,
      id: SERVER_ID,
      name: "거실 서버",
      version: "2.4.0",
      ...overrides,
    }),
    { status: 200, headers: { "content-type": "application/json" }, ...init },
  );
}


test("rejects redirects and identity changes before a session can be selected", async () => {
  await assert.rejects(
    probeEndpoint("https://media-box.local", {
      fetchImpl: async () => new Response(null, { status: 302, headers: { location: "http://other.local" } }),
    }),
    (error) => error instanceof ProbeError && error.code === "redirect",
  );

  await assert.rejects(
    probeEndpoint("https://media-box.local", {
      expectedId: SERVER_ID,
      fetchImpl: async () => discoveryResponse({ id: "22222222-2222-4222-8222-222222222222" }),
    }),
    (error) => error instanceof ProbeError && error.code === "identity_mismatch",
  );
});

test("rejects incompatible and oversized discovery responses", async () => {
  await assert.rejects(
    probeEndpoint("http://media-box.local", {
      fetchImpl: async () => discoveryResponse({ product: "something-else" }),
    }),
    (error) => error instanceof ProbeError && error.code === "incompatible_server",
  );

  await assert.rejects(
    probeEndpoint("http://media-box.local", {
      fetchImpl: async () => new Response("x".repeat(33 * 1024), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    }),
    (error) => error instanceof ProbeError && error.code === "response_too_large",
  );
});

test("rejected HTTP responses close their unfinished network body", async (t) => {
  let responseClosed;
  const closed = new Promise((resolve) => { responseClosed = resolve; });
  const server = createServer((_request, response) => {
    response.once("close", responseClosed);
    response.writeHead(503, { "content-type": "application/json" });
    response.write("{");
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(() => { server.closeAllConnections(); server.close(); });
  await assert.rejects(
    probeEndpoint(`http://127.0.0.1:${server.address().port}`),
    (error) => error instanceof ProbeError && error.code === "http_status",
  );
  let timeout;
  try {
    await Promise.race([
      closed,
      new Promise((_resolve, reject) => { timeout = setTimeout(() => reject(new Error("Rejected response kept its connection open")), 1000); }),
    ]);
  } finally { clearTimeout(timeout); }
});
