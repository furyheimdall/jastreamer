import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const preload = await readFile(new URL("../remote-preload.cjs", import.meta.url), "utf8");
const origin = "https://music.local:8443";

function loadPreload({ platform = "darwin", isMainFrame = true, href = `${origin}/`, expectedOrigin = origin, documentOrigin = new URL(href).origin } = {}) {
  const exposed = new Map();
  vm.runInNewContext(preload, {
    require(name) {
      assert.equal(name, "electron");
      return {
        contextBridge: { exposeInMainWorld: (key, value) => exposed.set(key, value) },
        ipcRenderer: {},
      };
    },
    process: {
      platform,
      isMainFrame,
      argv: expectedOrigin === null ? [] : [`--jastreamer-server-origin=${expectedOrigin}`],
    },
    window: { location: { href }, origin: documentOrigin },
    URL,
  });
  return exposed;
}

test("native preload exposes no capability to frames or unverified document origins", () => {
  for (const input of [
    { isMainFrame: false },
    { expectedOrigin: null },
    { documentOrigin: "null" },
    { href: "https://untrusted.local/" },
    { href: "http://music.local:8443/" },
    { href: "https://music.local/" },
    { href: "https://user:secret@music.local:8443/" },
    { href: `blob:${origin}/opaque` },
    { href: "about:blank" },
    { href: "file:///Applications/Jastreamer.app/Contents/Resources/app/index.html" },
    { platform: "linux" },
  ]) {
    assert.equal(loadPreload(input).size, 0, JSON.stringify(input));
  }
});

test("both desktop platforms expose the same frozen bridge only to a verified main document", () => {
  for (const platform of ["win32", "darwin"]) {
    const exposed = loadPreload({ platform, href: `${origin}/library?album=1#track` });
    assert.deepEqual([...exposed.keys()], ["JastreamerDesktopAudio"]);
    assert.equal(Object.isFrozen(exposed.get("JastreamerDesktopAudio")), true);
  }
});
