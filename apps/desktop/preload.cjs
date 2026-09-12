"use strict";

const { contextBridge, ipcRenderer } = require("electron");

const invoke = (channel, ...args) => ipcRenderer.invoke(channel, ...args);

contextBridge.exposeInMainWorld(
  "jastreamerDesktop",
  Object.freeze({
    getState: () => invoke("desktop:get-state"),
    refresh: () => invoke("desktop:refresh"),
    connect: (origin, expectedId = null) =>
      invoke("desktop:connect", { origin: String(origin), expectedId: expectedId ? String(expectedId) : null }),
    retry: () => invoke("desktop:retry"),
    changeServer: () => invoke("desktop:change-server"),
    setLanguage: (language) => invoke("desktop:set-language", String(language)),
    onState: (listener) => {
      if (typeof listener !== "function") throw new TypeError("listener must be a function");
      const handler = (_event, state) => listener(state);
      ipcRenderer.on("desktop:state", handler);
      return () => ipcRenderer.removeListener("desktop:state", handler);
    },
  }),
);
