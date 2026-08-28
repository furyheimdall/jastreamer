import { expect, test } from "bun:test";
import {
  NO_COMMON_MAJOR_STATUS,
  SELECTED_MAJOR_HEADER,
  SUPPORTED_MAJORS_HEADER,
  SUPPORTED_PROTOCOL_MAJORS,
  selectProtocolMajor,
} from "./protocol";

test("TypeScript negotiation constants select v3, fall back to v2, and require upgrade", () => {
  expect(SUPPORTED_PROTOCOL_MAJORS).toEqual([3, 2]);
  expect(SUPPORTED_MAJORS_HEADER).toBe("X-Jake-Supported-Protocol-Majors");
  expect(SELECTED_MAJOR_HEADER).toBe("X-Jake-Selected-Protocol-Major");
  expect(selectProtocolMajor(SUPPORTED_PROTOCOL_MAJORS, [3, 2])).toEqual({ kind: "selected", major: 3 });
  expect(selectProtocolMajor(SUPPORTED_PROTOCOL_MAJORS, [2])).toEqual({ kind: "selected", major: 2 });
  expect(selectProtocolMajor(SUPPORTED_PROTOCOL_MAJORS, [1])).toEqual({ kind: "upgrade_required", httpStatus: NO_COMMON_MAJOR_STATUS });
});
