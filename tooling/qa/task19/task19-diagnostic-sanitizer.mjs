const secretAssignment = /((?:"|')?(?:access[_-]?token|admin[_-]?token|authorization|cookie|password|setup[_-]?secret|secret|token|ticket|credential|private[_-]?key|pairing[_-]?code)(?:"|')?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^&#,\s;}\]]+)/gi;
const bearerCredential = /\bBearer\s+[^\s,;]+/gi;

export const isTask19SecretField = (key) => {
  const normalized = String(key).replaceAll(/[^a-z0-9]/gi, "").toLowerCase();
  return ["authorization", "cookie", "password", "secret", "token", "ticket", "credential", "privatekey", "pairingcode"]
    .some((marker) => normalized.includes(marker));
};

export const sanitizeTask19DiagnosticText = (value) => value
  .replace(bearerCredential, "Bearer [redacted]")
  .replace(secretAssignment, "$1[redacted]");

export const sanitizeTask19DiagnosticValue = (value, { key = "", redactCode = false } = {}) => {
  if (isTask19SecretField(key) || (redactCode && key === "code")) return "[redacted]";
  if (Array.isArray(value)) return value.map((item) => sanitizeTask19DiagnosticValue(item, { redactCode }));
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([field, item]) => [
      field,
      sanitizeTask19DiagnosticValue(item, { key: field, redactCode }),
    ]));
  }
  return typeof value === "string" ? sanitizeTask19DiagnosticText(value) : value;
};

export const sanitizeTask19EvidenceValue = (value, { key = "" } = {}) => {
  if (Array.isArray(value)) {
    return value.map((item) => sanitizeTask19EvidenceValue(item));
  }
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([field, item]) => [
      field,
      sanitizeTask19EvidenceValue(item, { key: field }),
    ]));
  }
  if (typeof value !== "string") return value;
  const sanitized = sanitizeTask19DiagnosticText(value);
  const normalizedKey = key.replaceAll(/[^a-z0-9]/gi, "").toLowerCase();
  const isPathField = normalizedKey.endsWith("path")
    || normalizedKey === "executablepath"
    || normalizedKey === "spawnfile";
  const isAbsolutePath = sanitized.startsWith("/")
    || /^[a-z]:[\\/]/i.test(sanitized);
  if (!isPathField || !isAbsolutePath) return sanitized;
  const basename = sanitized.replaceAll("\\", "/").split("/").at(-1);
  return `[host-path]/${basename}`;
};
