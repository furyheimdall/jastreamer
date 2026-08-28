import { randomUUID } from "node:crypto";

const bounded = async (promise, message) => {
  let timeout;
  const expired = new Promise((_, reject) => {
    timeout = setTimeout(() => reject(new Error(message)), 10_000);
  });
  try {
    return await Promise.race([promise, expired]);
  } finally {
    clearTimeout(timeout);
  }
};

export const subscribeAdminEvents = async (page) => {
  const suffix = randomUUID().replaceAll("-", "");
  const callbackName = `adminEvent${suffix}`;
  const socketName = `adminSocket${suffix}`;
  let resolveSnapshot;
  let rejectSnapshot;
  let resolveClose;
  const snapshotSignal = new Promise((resolve, reject) => {
    resolveSnapshot = resolve;
    rejectSnapshot = reject;
  });
  const closeSignal = new Promise((resolve) => { resolveClose = resolve; });
  const queued = [];
  const waiters = [];
  await page.exposeFunction(callbackName, (message) => {
    if (message.kind === "event") {
      if (message.event.type === "snapshot") {
        resolveSnapshot(message.event);
        return;
      }
      const index = waiters.findIndex((waiter) => waiter.resource === message.event.resource);
      if (index === -1) queued.push(message.event);
      else waiters.splice(index, 1)[0].resolve(message.event);
      return;
    }
    if (message.kind === "close") {
      resolveClose({ code: message.code, reason: message.reason, clean: message.clean });
      return;
    }
    rejectSnapshot(new Error("event socket failed"));
  });
  await page.evaluate(async ({ callbackName: callback, socketName: socketKey }) => {
    const token = sessionStorage.getItem("jastreamer-admin-token") || "";
    const response = await fetch("/api/v1/event-tickets", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) throw new Error(`event ticket failed: ${response.status}`);
    const { ticket } = await response.json();
    const socket = new WebSocket(`${location.origin.replace("https", "wss")}/api/v1/events?ticket=${encodeURIComponent(ticket)}`);
    window[socketKey] = socket;
    socket.onmessage = ({ data }) => window[callback]({ kind: "event", event: JSON.parse(data) });
    socket.onclose = ({ code, reason, wasClean }) => window[callback]({ kind: "close", code, reason, clean: wasClean });
    socket.onerror = () => window[callback]({ kind: "error" });
  }, { callbackName, socketName });
  const initial = await bounded(snapshotSignal, "event snapshot timeout");
  return {
    initial,
    waitForClose() {
      return bounded(closeSignal, "event close timeout");
    },
    subscribe(resource) {
      const queuedIndex = queued.findIndex((event) => event.resource === resource);
      if (queuedIndex !== -1) {
        return { signal: Promise.resolve(queued.splice(queuedIndex, 1)[0]), unsubscribe() {} };
      }
      let waiter;
      const signal = new Promise((resolve) => {
        waiter = { resource, resolve };
        waiters.push(waiter);
      });
      return {
        signal,
        unsubscribe() {
          const index = waiters.indexOf(waiter);
          if (index !== -1) waiters.splice(index, 1);
        },
      };
    },
    async close() {
      await page.evaluate((socketKey) => window[socketKey]?.close(), socketName);
    },
  };
};
