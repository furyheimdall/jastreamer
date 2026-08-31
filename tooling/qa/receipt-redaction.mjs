const SECRET_KEY = /(secret|token|password|credential|private.?key|pairing.?code|authorization|cookie)/i;
const SECRET_VALUE = /(?:\bBearer\s+[A-Za-z0-9._~+\/-]+=*|\beyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|(?:password|credential|api[_-]?key|secret|token|ticket|pairing[_-]?code)\s*[=:]\s*\S+)/i;
const ABSOLUTE_PATH = /(?:^|[\s=:'"])(?:\/[A-Za-z0-9._-][^\s'"]*|[A-Za-z]:\\[^\s'"]+|\\\\[^\\\s]+\\[^\s]+)/;
const PRIVATE_IPV4 = /\b(?:127\.\d{1,3}\.\d{1,3}\.\d{1,3}|169\.254\.\d{1,3}\.\d{1,3}|10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b/;
const LAN_IDENTITY = /(?:\buuid:|\b(?:fe80|fc[0-9a-f]|fd[0-9a-f]):|\b(?:[0-9a-f]{2}:){5}[0-9a-f]{2}\b|\b(?:endpoint|device)[-_ ]?id\s*[=:]|\braw-device-[a-z0-9-]+\b|\b(?:localhost|ip6-localhost)\b|(?:\[?::1\]?)|\b[a-z0-9-]+\.(?:local|lan|internal|private|home|corp)\b)/i;
const NON_REAL = /\b(?:mock|fake)(?:[-_ ]?only|[-_ ]?audio|[-_ ]?backend)?\b/i;

export const findUnsafeEvidence = (value, path = "$") => {
  if (typeof value === "string") {
    if (SECRET_VALUE.test(value)) return { code: "SECRET_PRESENT", path };
    const productRoute = /^\/(?:healthz|api\/v1\/[A-Za-z0-9:{}._~/?=&-]+)$/.test(value) && !value.includes("..");
    if (!productRoute && !/^\/[A-Za-z]$/.test(value) && ABSOLUTE_PATH.test(value)) return { code: "ABSOLUTE_PATH_PRESENT", path };
    if (PRIVATE_IPV4.test(value) || LAN_IDENTITY.test(value)) return { code: "LAN_IDENTITY_PRESENT", path };
    if (NON_REAL.test(value)) return { code: "NON_REAL_EVIDENCE", path };
    return undefined;
  }
  if (Array.isArray(value)) { for (const [index, item] of value.entries()) { const issue = findUnsafeEvidence(item, `${path}[${index}]`); if (issue) return issue; } return undefined; }
  if (value !== null && typeof value === "object") for (const [key, item] of Object.entries(value)) { const child = path === "$" ? key : `${path}.${key}`; if (SECRET_KEY.test(key)) return { code: "SECRET_PRESENT", path: child }; if (NON_REAL.test(key)) return { code: "NON_REAL_EVIDENCE", path: child }; if (/^(?:sha256|keyId|signature)$/i.test(key)) continue; const issue = findUnsafeEvidence(item, child); if (issue) return issue; }
  return undefined;
};
