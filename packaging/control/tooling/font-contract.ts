#!/usr/bin/env bun
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const EXPECTED = Object.freeze({
  family: "Noto Sans KR",
  commit: "4efc2774c63917927efe769ca845def6bd6debae",
  fontSize: 10_414_588,
  fontBlobSha1: "b386890ba945e1f39448a6b59f20c5d194f58808",
  fontSha256: "194018e6b2b293a7964f037b25c0249ce1418bc9ab3c971060a03aa57861e252",
  licenseBlobSha1: "1c9f43281b8f216c5461fe9ac729afbade7724e4",
  licenseSha256: "1c05c68c34f9708415aada51f17e1b0092d2cea709bf4a94cd38114f9e73d7d9",
  representativeText: "안녕하세요 재생 목록",
} as const);

type Table = Readonly<{ offset: number; length: number }>;
export type ControlFontEvidence = Readonly<{ family: string; fontSha256: string; licenseSha256: string; weightAxis: readonly [number, number, number]; representativeText: string }>;

const sha256 = (bytes: Buffer): string => createHash("sha256").update(bytes).digest("hex");
const gitBlobSha1 = (bytes: Buffer): string => createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
const fail = (code: string): never => { throw new Error(code); };
const isRecord = (value: unknown): value is Readonly<Record<string, unknown>> => typeof value === "object" && value !== null && !Array.isArray(value);
const record = (value: unknown): Readonly<Record<string, unknown>> => isRecord(value) ? value : fail("CONTROL_FONT_METADATA_INVALID");
const text = (value: unknown): string => typeof value === "string" ? value : fail("CONTROL_FONT_METADATA_INVALID");
const integer = (value: unknown): number => Number.isSafeInteger(value) ? Number(value) : fail("CONTROL_FONT_METADATA_INVALID");
const fixed = (bytes: Buffer, offset: number): number => bytes.readInt32BE(offset) / 65_536;
const tablesOf = (bytes: Buffer): ReadonlyMap<string, Table> => {
  if (bytes.length < 12) fail("CONTROL_FONT_TTF_INVALID");
  const tables = new Map<string, Table>(); const count = bytes.readUInt16BE(4);
  for (let index = 0; index < count; index += 1) { const entry = 12 + index * 16; if (entry + 16 > bytes.length) fail("CONTROL_FONT_TTF_INVALID"); const tag = bytes.toString("ascii", entry, entry + 4); const offset = bytes.readUInt32BE(entry + 8); const length = bytes.readUInt32BE(entry + 12); if (offset + length > bytes.length) fail("CONTROL_FONT_TTF_INVALID"); tables.set(tag, { offset, length }); }
  return tables;
};
const table = (tables: ReadonlyMap<string, Table>, tag: string): Table => tables.get(tag) ?? fail("CONTROL_FONT_TTF_INVALID");
const utf16be = (bytes: Buffer): string => { if (bytes.length % 2 !== 0) fail("CONTROL_FONT_TTF_INVALID"); const swapped = Buffer.alloc(bytes.length); for (let index = 0; index < bytes.length; index += 2) { swapped[index] = bytes[index + 1] ?? 0; swapped[index + 1] = bytes[index] ?? 0; } return swapped.toString("utf16le"); };
const families = (bytes: Buffer, name: Table): ReadonlySet<string> => {
  const count = bytes.readUInt16BE(name.offset + 2); const storage = name.offset + bytes.readUInt16BE(name.offset + 4); const result = new Set<string>();
  for (let index = 0; index < count; index += 1) { const entry = name.offset + 6 + index * 12; const platform = bytes.readUInt16BE(entry); const nameId = bytes.readUInt16BE(entry + 6); const length = bytes.readUInt16BE(entry + 8); const offset = storage + bytes.readUInt16BE(entry + 10); if ((nameId === 1 || nameId === 16) && platform === 3 && offset + length <= bytes.length) result.add(utf16be(bytes.subarray(offset, offset + length))); }
  return result;
};
const weightAxis = (bytes: Buffer, fvar: Table): readonly [number, number, number] => {
  const axesOffset = bytes.readUInt16BE(fvar.offset + 4); const axisCount = bytes.readUInt16BE(fvar.offset + 8); const axisSize = bytes.readUInt16BE(fvar.offset + 10);
  for (let index = 0; index < axisCount; index += 1) { const offset = fvar.offset + axesOffset + index * axisSize; if (bytes.toString("ascii", offset, offset + 4) === "wght") return Object.freeze([fixed(bytes, offset + 4), fixed(bytes, offset + 8), fixed(bytes, offset + 12)]); }
  return fail("CONTROL_FONT_WEIGHT_AXIS_INVALID");
};
const format12Covers = (bytes: Buffer, offset: number, codepoint: number): boolean => {
  const groups = bytes.readUInt32BE(offset + 12);
  for (let index = 0; index < groups; index += 1) { const group = offset + 16 + index * 12; const start = bytes.readUInt32BE(group); const end = bytes.readUInt32BE(group + 4); if (codepoint < start) return false; if (codepoint <= end) return true; }
  return false;
};
const covers = (bytes: Buffer, cmap: Table, codepoint: number): boolean => {
  const count = bytes.readUInt16BE(cmap.offset + 2);
  for (let index = 0; index < count; index += 1) { const recordOffset = cmap.offset + 4 + index * 8; const subtable = cmap.offset + bytes.readUInt32BE(recordOffset + 4); if (subtable + 16 <= bytes.length && bytes.readUInt16BE(subtable) === 12 && format12Covers(bytes, subtable, codepoint)) return true; }
  return false;
};

export function verifyControlFont(controlRoot: string): ControlFontEvidence {
  const root = resolve(controlRoot); const font = readFileSync(resolve(root, "assets/fonts/noto_sans_kr/NotoSansKR-wght.ttf")); const license = readFileSync(resolve(root, "assets/fonts/noto_sans_kr/OFL.txt")); const metadata = record(JSON.parse(readFileSync(resolve(root, "assets/fonts/noto_sans_kr/source.json"), "utf8")));
  const fontMetadata = record(metadata["font"]); const licenseMetadata = record(metadata["licenseFile"]); const axisMetadata = record(fontMetadata["weightAxis"]);
  if (text(metadata["family"]) !== EXPECTED.family || text(metadata["commit"]) !== EXPECTED.commit || text(metadata["license"]) !== "OFL-1.1" || text(metadata["representativeKoreanText"]) !== EXPECTED.representativeText || integer(fontMetadata["size"]) !== EXPECTED.fontSize || text(fontMetadata["gitBlobSha1"]) !== EXPECTED.fontBlobSha1 || text(fontMetadata["sha256"]) !== EXPECTED.fontSha256 || text(licenseMetadata["gitBlobSha1"]) !== EXPECTED.licenseBlobSha1 || text(licenseMetadata["sha256"]) !== EXPECTED.licenseSha256 || integer(axisMetadata["minimum"]) !== 100 || integer(axisMetadata["default"]) !== 100 || integer(axisMetadata["maximum"]) !== 900) fail("CONTROL_FONT_METADATA_DRIFT");
  if (font.length !== EXPECTED.fontSize || gitBlobSha1(font) !== EXPECTED.fontBlobSha1 || sha256(font) !== EXPECTED.fontSha256) fail("CONTROL_FONT_BYTES_DRIFT");
  if (gitBlobSha1(license) !== EXPECTED.licenseBlobSha1 || sha256(license) !== EXPECTED.licenseSha256 || !license.includes("SIL OPEN FONT LICENSE Version 1.1")) fail("CONTROL_FONT_LICENSE_DRIFT");
  const tables = tablesOf(font); if (!families(font, table(tables, "name")).has(EXPECTED.family)) fail("CONTROL_FONT_FAMILY_INVALID"); const axis = weightAxis(font, table(tables, "fvar")); if (axis[0] !== 100 || axis[1] !== 100 || axis[2] !== 900) fail("CONTROL_FONT_WEIGHT_AXIS_INVALID"); for (const character of EXPECTED.representativeText) if (character !== " " && !covers(font, table(tables, "cmap"), character.codePointAt(0) ?? fail("CONTROL_FONT_GLYPH_INVALID"))) fail("CONTROL_FONT_GLYPH_INVALID");
  return Object.freeze({ family: EXPECTED.family, fontSha256: EXPECTED.fontSha256, licenseSha256: EXPECTED.licenseSha256, weightAxis: axis, representativeText: EXPECTED.representativeText });
}

if (import.meta.main) {
  const root = process.argv[2] ?? fail("CONTROL_FONT_VERIFY_USAGE");
  console.log(JSON.stringify(verifyControlFont(root)));
}
