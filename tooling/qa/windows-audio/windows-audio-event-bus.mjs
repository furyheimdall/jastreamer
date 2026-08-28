export class NativeEventBus {
  #waiters = new Map();

  subscribe(name, signal) {
    if (signal.aborted) throw new Error(`EVENT_TIMEOUT:${name}`);
    let resolveSignal;
    let rejectSignal;
    const promise = new Promise((resolve, reject) => { resolveSignal = resolve; rejectSignal = reject; });
    const waiter = { resolve: resolveSignal, reject: rejectSignal };
    const waiting = this.#waiters.get(name) ?? new Set();
    waiting.add(waiter);
    this.#waiters.set(name, waiting);
    const abort = () => {
      waiting.delete(waiter);
      rejectSignal(new Error(`EVENT_TIMEOUT:${name}`));
    };
    signal.addEventListener("abort", abort, { once: true });
    return {
      signal: promise,
      unsubscribe: () => {
        signal.removeEventListener("abort", abort);
        waiting.delete(waiter);
      },
    };
  }

  emit(name, value) {
    const waiting = this.#waiters.get(name);
    if (waiting === undefined) return;
    for (const waiter of waiting) waiter.resolve(value);
    waiting.clear();
  }
}

export const pumpJsonLines = async (stream, bus) => {
  const reader = stream.pipeThrough(new TextDecoderStream()).getReader();
  let pending = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    pending += value;
    let newline = pending.indexOf("\n");
    while (newline >= 0) {
      const line = pending.slice(0, newline); pending = pending.slice(newline + 1);
      if (line !== "") {
        const event = JSON.parse(line);
        if (typeof event.event !== "string") throw new Error("PEER_EVENT_INVALID");
        bus.emit(event.event, event);
      }
      newline = pending.indexOf("\n");
    }
  }
  if (pending !== "") throw new Error("PEER_EVENT_TRUNCATED");
};
