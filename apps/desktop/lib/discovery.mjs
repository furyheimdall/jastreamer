import { isIP } from "node:net";
import { networkInterfaces } from "node:os";
import { Bonjour } from "bonjour-service";
import multicastDNS from "multicast-dns";
import { probeEndpoint } from "./probe.mjs";
import { normalizeEndpoint, normalizeServerId } from "./security.mjs";
import { getLanguage, t } from "./i18n.mjs";

const MAX_DISCOVERED_SERVICES = 64;
const MAX_PARALLEL_PROBES = 4;
const DISCOVERY_QUERY_INTERVAL_MS = 5_000;
const DEFAULT_PROBE_INTERVAL_MS = 60_000;
const DISCOVERY_SERVICE = "_jastreamer._tcp.local";

function ipv4InterfaceAddresses() {
  const addresses = new Set();
  for (const interfaces of Object.values(networkInterfaces())) {
    for (const entry of interfaces ?? []) {
      if (!entry.internal && (entry.family === 4 || entry.family === "IPv4")) {
        addresses.add(entry.address);
      }
    }
  }
  return addresses;
}

function txtValue(value) {
  if (Buffer.isBuffer(value)) return value.toString("utf8");
  return typeof value === "string" ? value : "";
}

function cleanText(value, maximum) {
  const text = txtValue(value).trim();
  return text && text.length <= maximum && !/[\u0000-\u001f\u007f]/.test(text) ? text : null;
}

function isEligibleLanAddress(address) {
  const plain = address.split("%")[0].toLowerCase();
  const family = isIP(plain);
  if (family === 4) {
    const [first, second] = plain.split(".").map(Number);
    return first === 10 || (first === 172 && second >= 16 && second <= 31) ||
      (first === 192 && second === 168) || (first === 169 && second === 254);
  }
  if (family === 6) {
    return /^(?:fc|fd|fe[89ab])/.test(plain);
  }
  return false;
}

function endpointForHost(scheme, host, port) {
  const bareHost = host.endsWith(".") ? host.slice(0, -1) : host;
  const formattedHost = isIP(bareHost) === 6 ? `[${bareHost}]` : bareHost;
  try {
    return normalizeEndpoint(`${scheme}://${formattedHost}:${port}`, { allowImplicitHttp: false });
  } catch {
    return null;
  }
}

function parseService(service) {
  const txt = service?.txt ?? {};
  if (txtValue(txt.product) && txtValue(txt.product) !== "jastreamer") return null;
  if (txtValue(txt.protocol) !== "1" || txtValue(txt.path) !== "/") return null;

  let id;
  try {
    id = normalizeServerId(txtValue(txt.id));
  } catch {
    return null;
  }

  const scheme = txtValue(txt.scheme).toLowerCase();
  const name = cleanText(txt.name, 128) ?? cleanText(service.name, 128);
  const version = cleanText(txt.version, 64);
  const port = Number(service.port);
  if ((scheme !== "http" && scheme !== "https") || !name || !version || !Number.isInteger(port)) {
    return null;
  }
  if (port < 1 || port > 65_535) return null;

  const addresses = Array.isArray(service.addresses) ? service.addresses : [];
  const eligibleAddresses = addresses
    .map((address) => String(address).trim())
    .filter((address) => isEligibleLanAddress(address) && !address.includes("%"));
  const host = typeof service.host === "string" ? service.host.trim() : "";
  const localHost = host.toLowerCase().endsWith(".local") || host.toLowerCase().endsWith(".local.") ? host : null;

  const endpointHosts = scheme === "https"
    ? [localHost, ...eligibleAddresses]
    : [...eligibleAddresses, localHost];
  const origins = [...new Set(endpointHosts.filter(Boolean).map((candidate) => endpointForHost(scheme, candidate, port)).filter(Boolean))].slice(0, 6);
  if (!origins.length) return null;

  return { id, name, version, scheme, origins };
}

function serviceKey(service) {
  return String(service?.fqdn || `${service?.name ?? ""}|${service?.host ?? ""}|${service?.port ?? ""}`);
}

export class LanDiscovery {
  #bonjour;
  #browser = null;
  #querySockets = new Map();
  #records = new Map();
  #queue = [];
  #active = 0;
  #controllers = new Set();
  #timer = null;
  #queryTimer = null;
  #stopped = true;
  #onChange;
  #probe;
  #intervalMs;
  #sequence = 0;

  constructor({ onChange = () => {}, probe = probeEndpoint, intervalMs = DEFAULT_PROBE_INTERVAL_MS } = {}) {
    this.#bonjour = new Bonjour();
    this.#onChange = onChange;
    this.#probe = probe;
    this.#intervalMs = intervalMs;
  }

  start() {
    if (!this.#stopped) return;
    this.#stopped = false;
    this.#browser = this.#bonjour.find(
      { type: "jastreamer", protocol: "tcp" },
      (service) => this.#serviceUp(service),
    );
    this.#browser.on("down", (service) => this.#serviceDown(service));
    this.#browser.on("txt-update", (service) => this.#serviceUp(service));
    this.#browser.on("srv-update", (service) => this.#serviceUp(service));
    this.#queryAllInterfaces();
    this.#queryTimer = setInterval(() => {
      this.#browser?.expire?.();
      this.#queryAllInterfaces();
    }, DISCOVERY_QUERY_INTERVAL_MS);
    this.#queryTimer.unref?.();
    this.#timer = setInterval(() => {
      this.#scheduleAll();
    }, this.#intervalMs);
    this.#timer.unref?.();
  }

  refresh() {
    this.#browser?.expire?.();
    this.#queryAllInterfaces();
    this.#scheduleAll();
  }

  snapshot() {
    return [...this.#records.values()]
      .map(({ generation: _generation, queued: _queued, origins: _origins, messageKey, messageParams, ...record }) => ({
        ...record,
        message: t(messageKey, messageParams),
      }))
      .sort((left, right) => {
        if (left.availability !== right.availability) return left.availability === "available" ? -1 : 1;
        return left.name.localeCompare(right.name, getLanguage() === "ko" ? "ko-KR" : "en-US");
      });
  }

  stop() {
    this.#stopped = true;
    clearInterval(this.#timer);
    clearInterval(this.#queryTimer);
    this.#queryTimer = null;
    this.#timer = null;
    this.#queue = [];
    for (const controller of this.#controllers) controller.abort();
    this.#controllers.clear();
    this.#browser?.stop?.();
    this.#browser = null;
    for (const socket of this.#querySockets.values()) socket.destroy();
    this.#querySockets.clear();
    this.#bonjour.destroy();
  }

  #queryAllInterfaces() {
    if (this.#stopped) return;
    const addresses = ipv4InterfaceAddresses();
    for (const [address, socket] of this.#querySockets) {
      if (addresses.has(address)) continue;
      this.#querySockets.delete(address);
      socket.destroy();
    }
    for (const address of addresses) {
      let socket = this.#querySockets.get(address);
      if (!socket) {
        socket = multicastDNS({ interface: address, bind: "0.0.0.0" });
        this.#querySockets.set(address, socket);
        socket.on("error", () => {
          if (this.#querySockets.get(address) === socket) this.#querySockets.delete(address);
          socket.destroy();
        });
      }
      socket.query(DISCOVERY_SERVICE, "PTR");
    }
  }

  #serviceUp(service) {
    const parsed = parseService(service);
    if (!parsed) return;

    const key = serviceKey(service);
    if (!this.#records.has(key) && this.#records.size >= MAX_DISCOVERED_SERVICES) return;
    const previous = this.#records.get(key);
    this.#records.set(key, {
      key,
      id: parsed.id,
      name: parsed.name,
      version: parsed.version,
      origin: previous?.origin ?? parsed.origins[0],
      origins: parsed.origins,
      availability: "checking",
      messageKey: "status.checking",
      connectable: false,
      lastSeen: Date.now(),
      generation: ++this.#sequence,
      queued: previous?.queued ?? false,
    });
    this.#enqueue(key);
    this.#emit();
  }

  #serviceDown(service) {
    const key = serviceKey(service);
    if (!this.#records.delete(key)) return;
    this.#queue = this.#queue.filter((queuedKey) => queuedKey !== key);
    this.#emit();
  }

  #scheduleAll() {
    for (const [key, record] of this.#records) {
      record.availability = "checking";
      record.messageKey = "status.rechecking";
      record.connectable = false;
      record.generation = ++this.#sequence;
      this.#enqueue(key);
    }
    this.#emit();
  }

  #enqueue(key) {
    const record = this.#records.get(key);
    if (!record || record.queued) return;
    record.queued = true;
    this.#queue.push(key);
    this.#pump();
  }

  #pump() {
    while (!this.#stopped && this.#active < MAX_PARALLEL_PROBES && this.#queue.length) {
      const key = this.#queue.shift();
      const record = this.#records.get(key);
      if (!record) continue;
      record.queued = false;
      this.#active += 1;
      this.#probeRecord(key, record.generation).finally(() => {
        this.#active -= 1;
        this.#pump();
      });
    }
  }

  async #probeRecord(key, generation) {
    const record = this.#records.get(key);
    if (!record) return;

    const controller = new AbortController();
    this.#controllers.add(controller);
    let lastError = null;
    try {
      for (const origin of record.origins) {
        try {
          const metadata = await this.#probe(origin, {
            expectedId: record.id,
            timeoutMs: 3_500,
            signal: controller.signal,
          });
          const current = this.#records.get(key);
          if (!current || current.generation !== generation) return;
          Object.assign(current, {
            id: metadata.id,
            name: metadata.name,
            version: metadata.version,
            origin: metadata.origin,
            availability: "available",
            messageKey: "status.available",
            connectable: true,
            lastSeen: Date.now(),
          });
          this.#emit();
          return;
        } catch (error) {
          lastError = error;
          if (controller.signal.aborted) return;
        }
      }

      const current = this.#records.get(key);
      if (!current || current.generation !== generation) return;
      current.availability = "unavailable";
      current.connectable = false;
      current.messageKey = lastError?.messageKey ?? "probe.unknown";
      current.messageParams = lastError?.params;
      this.#emit();
    } finally {
      this.#controllers.delete(controller);
    }
  }

  #emit() {
    if (!this.#stopped) this.#onChange(this.snapshot());
  }
}
