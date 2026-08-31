export const TASK19_SIDECAR_PROTOCOL_VERSION = 2;
export const TASK19_SIDECAR_MAX_MESSAGE_BYTES = 1_048_576;
export const TASK19_SIDECAR_MAX_DIAGNOSTIC_BYTES = 524_288;

export class Task19SidecarError extends Error {
  constructor(code, detail) { super(code); this.name = "Task19SidecarError"; this.code = code; this.detail = detail; }
}

export const exactFields = (value, required, optional = []) => {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Task19SidecarError("TASK19_SIDECAR_MESSAGE_INVALID");
  const accepted = new Set([...required, ...optional]);
  for (const field of Object.keys(value)) if (!accepted.has(field)) throw new Task19SidecarError("TASK19_SIDECAR_UNKNOWN_FIELD", field);
  for (const field of required) if (!Object.hasOwn(value, field)) throw new Task19SidecarError("TASK19_SIDECAR_FIELD_MISSING", field);
  return value;
};

export const encodeLine = (value) => {
  const line = `${JSON.stringify(value)}\n`; if (Buffer.byteLength(line) > TASK19_SIDECAR_MAX_MESSAGE_BYTES) throw new Task19SidecarError("TASK19_SIDECAR_MESSAGE_OVERSIZED"); return line;
};

const rejectDuplicateFields = (text) => {
  const stack = []; for (let index = 0; index < text.length; index += 1) { const token = text[index]; if (token === "{") { stack.push(new Set()); continue; } if (token === "[") { stack.push(undefined); continue; } if (token === "}" || token === "]") { stack.pop(); continue; } if (token !== '"') continue; let end = index + 1; for (; end < text.length; end += 1) { if (text[end] === "\\") { end += 1; continue; } if (text[end] === '"') break; } if (end >= text.length) throw new Task19SidecarError("TASK19_SIDECAR_JSON_INVALID"); let next = end + 1; while (/\s/.test(text[next] ?? "")) next += 1; const fields = stack.at(-1); if (text[next] === ":" && fields !== undefined) { const field = JSON.parse(text.slice(index, end + 1)); if (fields.has(field)) throw new Task19SidecarError("TASK19_SIDECAR_DUPLICATE_FIELD", field); fields.add(field); } index = end; }
};

export const createJsonlDecoder = (consume, fail) => {
  let buffered = Buffer.alloc(0); let failed = false;
  return (chunk) => {
    if (failed) return; buffered = Buffer.concat([buffered, chunk]);
    if (buffered.length > TASK19_SIDECAR_MAX_MESSAGE_BYTES) { failed = true; fail(new Task19SidecarError("TASK19_SIDECAR_MESSAGE_OVERSIZED")); return; }
    for (;;) {
      const newline = buffered.indexOf(10); if (newline < 0) return;
      const line = buffered.subarray(0, newline); buffered = buffered.subarray(newline + 1);
      if (line.length === 0) { failed = true; fail(new Task19SidecarError("TASK19_SIDECAR_MESSAGE_INVALID")); return; }
      try { const text = line.toString("utf8"); rejectDuplicateFields(text); consume(JSON.parse(text)); } catch (error) { failed = true; fail(error instanceof Task19SidecarError ? error : new Task19SidecarError("TASK19_SIDECAR_JSON_INVALID")); return; }
    }
  };
};
