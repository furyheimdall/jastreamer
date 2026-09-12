import assert from "node:assert/strict";
import test from "node:test";
import {
  isSameServerNavigation,
  isTrustedShellSender,
  normalizeEndpoint,
  sessionPartitionFor,
} from "../lib/security.mjs";

const SERVER_A = "11111111-1111-4111-8111-111111111111";
const SERVER_B = "22222222-2222-4222-8222-222222222222";

test("normalizes only credential-free HTTP(S) root origins", () => {
  assert.equal(normalizeEndpoint("media-box.local:8080"), "http://media-box.local:8080");
  assert.equal(normalizeEndpoint("HTTPS://MEDIA-BOX.LOCAL:443/"), "https://media-box.local");

  for (const invalid of [
    "ftp://media-box.local",
    "http://user:secret@media-box.local",
    "http://media-box.local/player",
    "http://media-box.local/?token=secret",
    "http://media-box.local/#fragment",
  ]) {
    assert.throws(() => normalizeEndpoint(invalid));
  }
});

test("partitions sessions by both verified identity and normalized origin", () => {
  const base = sessionPartitionFor(SERVER_A, "http://192.168.1.20:8080");
  assert.equal(base, sessionPartitionFor(SERVER_A.toUpperCase(), "http://192.168.1.20:8080/"));
  assert.notEqual(base, sessionPartitionFor(SERVER_A, "http://192.168.1.20:8081"));
  assert.notEqual(base, sessionPartitionFor(SERVER_B, "http://192.168.1.20:8080"));
});

test("remote top-level navigation remains on the selected server origin", () => {
  const origin = "https://media-box.local:8443";
  assert.equal(isSameServerNavigation(`${origin}/albums/7?view=grid#top`, origin), true);
  assert.equal(isSameServerNavigation("https://other.local/", origin), false);
  assert.equal(isSameServerNavigation("javascript:alert(1)", origin), false);
  assert.equal(isSameServerNavigation("https://user:secret@media-box.local:8443/", origin), false);
});

test("shell IPC accepts equivalent file URL encoding without trusting other documents or frames", () => {
  const shellUrl = "file:///C:/Users/RUNNER%7E1/portable%20with%20spaces/index.html";
  const mainFrame = { url: shellUrl.replace("%7E", "~") };
  const shellContents = { mainFrame };
  const event = { sender: shellContents, senderFrame: mainFrame };
  assert.equal(isTrustedShellSender(event, shellContents, shellUrl), true);
  assert.equal(isTrustedShellSender({ ...event, sender: {} }, shellContents, shellUrl), false);
  assert.equal(isTrustedShellSender({ ...event, senderFrame: { ...mainFrame } }, shellContents, shellUrl), false);

  for (const otherDocument of [
    "https://other.local/index.html",
    shellUrl.replace("index.html", "other.html"),
    `${shellUrl}?untrusted=1`,
    `${shellUrl}#untrusted`,
    shellUrl.replace("portable%20", "portable%2F"),
  ]) {
    mainFrame.url = otherDocument;
    assert.equal(isTrustedShellSender(event, shellContents, shellUrl), false, otherDocument);
  }
});
