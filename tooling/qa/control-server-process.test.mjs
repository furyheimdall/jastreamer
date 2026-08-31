import { describe, expect, test } from "bun:test";
import { createTodo13ReadinessParser } from "./control-server-process.mjs";

describe("Control QA Server readiness parsing", () => {
  test("recognizes a readiness line across every stream chunk boundary", () => {
    const line = "ready https://127.0.0.1:8443 fingerprint=abcdef012345\n";
    for (let split = 1; split < line.length; split += 1) {
      const parser = createTodo13ReadinessParser();
      expect(parser.push(line.slice(0, split))).toBeNull();
      expect(parser.push(line.slice(split))).toEqual({
        origin: "https://127.0.0.1:8443",
        fingerprint: "abcdef012345",
      });
    }
  });

  test("bounds unterminated readiness output", () => {
    const parser = createTodo13ReadinessParser({ maxBytes: 8 });
    expect(() => parser.push("123456789")).toThrow(
      "SERVER_READINESS_OUTPUT_OVERSIZED",
    );
  });
});
