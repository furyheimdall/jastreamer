"use strict";

const { contextBridge, ipcRenderer } = require("electron");

if (process.platform === "win32") {
  const listeners = new Map();
  contextBridge.exposeInMainWorld(
    "JastreamerWindowsAudio",
    Object.freeze({
      request(action, args = {}) {
        if (typeof action !== "string") return Promise.reject(new TypeError("action must be a string"));
        if (!args || typeof args !== "object" || Array.isArray(args)) {
          return Promise.reject(new TypeError("args must be an object"));
        }
        return ipcRenderer.invoke("windows-audio:request", action, args);
      },
      subscribe(listener) {
        if (typeof listener !== "function") throw new TypeError("listener must be a function");
        const previous = listeners.get(listener);
        if (previous) ipcRenderer.removeListener("windows-audio:state", previous);
        const wrapped = (_event, state) => listener(state);
        listeners.set(listener, wrapped);
        ipcRenderer.on("windows-audio:state", wrapped);
        return () => {
          if (listeners.get(listener) !== wrapped) return;
          listeners.delete(listener);
          ipcRenderer.removeListener("windows-audio:state", wrapped);
        };
      },
    }),
  );
}
