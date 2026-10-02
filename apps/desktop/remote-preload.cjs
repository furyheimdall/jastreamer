"use strict";

const { contextBridge, ipcRenderer } = require("electron");

// The main process only attaches this preload after probing the Server identity.
// Recheck the document here so neither frames nor blocked navigations receive a bridge.
const originArgument = process.argv.find((value) => value.startsWith("--jastreamer-server-origin="));
let trustedDocument = false;
try {
  const url = new URL(window.location.href);
  trustedDocument = process.isMainFrame === true
    && ["http:", "https:"].includes(url.protocol)
    && window.origin === url.origin
    && !url.username && !url.password
    && url.origin === originArgument?.slice("--jastreamer-server-origin=".length);
} catch {}

if (trustedDocument && (process.platform === "win32" || process.platform === "darwin")) {
  const listeners = new Map();
  contextBridge.exposeInMainWorld(
    "JastreamerDesktopAudio",
    Object.freeze({
      request(action, args = {}) {
        if (typeof action !== "string") return Promise.reject(new TypeError("action must be a string"));
        if (!args || typeof args !== "object" || Array.isArray(args)) {
          return Promise.reject(new TypeError("args must be an object"));
        }
        return ipcRenderer.invoke("desktop-audio:request", action, args);
      },
      subscribe(listener) {
        if (typeof listener !== "function") throw new TypeError("listener must be a function");
        const previous = listeners.get(listener);
        if (previous) ipcRenderer.removeListener("desktop-audio:state", previous);
        const wrapped = (_event, state) => listener(state);
        listeners.set(listener, wrapped);
        ipcRenderer.on("desktop-audio:state", wrapped);
        return () => {
          if (listeners.get(listener) !== wrapped) return;
          listeners.delete(listener);
          ipcRenderer.removeListener("desktop-audio:state", wrapped);
        };
      },
    }),
  );
}
