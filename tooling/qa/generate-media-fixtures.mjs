import { createHash } from "node:crypto";
import { copyFile, mkdir, readFile } from "node:fs/promises";
import { isAbsolute, join } from "node:path";
import { pathToFileURL } from "node:url";

const recipe = JSON.parse(await readFile(new URL("../fixtures/e2e/media-fixtures.v1.json", import.meta.url), "utf8"));
const SAMPLE_RATE = recipe.sampleRateHz;
const AMPLITUDE = recipe.amplitude;
const CODECS = recipe.codecs;
const SIGNALS = recipe.signals.map((signal) => ({
  name: signal.id,
  durationSeconds: signal.durationSeconds,
  frequencyAt: (sample) => signal.segmentFrequenciesHz[Math.floor(sample / SAMPLE_RATE)],
}));

export class FixtureGenerationError extends Error {
  name = "FixtureGenerationError";
  constructor(code, detail = "") {
    super(detail === "" ? code : `${code}:${detail}`);
    this.code = code;
  }
}

const writeWav = async (path, signal) => {
  const sampleCount = SAMPLE_RATE * signal.durationSeconds;
  const dataSize = sampleCount * 2;
  const output = Buffer.alloc(44 + dataSize);
  output.write("RIFF", 0);
  output.writeUInt32LE(36 + dataSize, 4);
  output.write("WAVEfmt ", 8);
  output.writeUInt32LE(16, 16);
  output.writeUInt16LE(1, 20);
  output.writeUInt16LE(1, 22);
  output.writeUInt32LE(SAMPLE_RATE, 24);
  output.writeUInt32LE(SAMPLE_RATE * 2, 28);
  output.writeUInt16LE(2, 32);
  output.writeUInt16LE(16, 34);
  output.write("data", 36);
  output.writeUInt32LE(dataSize, 40);
  for (let sample = 0; sample < sampleCount; sample += 1) {
    const frequency = signal.frequencyAt(sample);
    const value = Math.round(AMPLITUDE * Math.sin(2 * Math.PI * frequency * sample / SAMPLE_RATE));
    output.writeInt16LE(value, 44 + sample * 2);
  }
  await Bun.write(path, output);
};

const codecArguments = (codec) => {
  switch (codec) {
    case "flac": return ["-c:a", "flac", "-compression_level", "8"];
    case "mp3": return ["-c:a", "libmp3lame", "-b:a", "128k", "-write_xing", "0"];
    case "ogg": return ["-c:a", "libvorbis", "-q:a", "4"];
    case "opus": return ["-c:a", "libopus", "-b:a", "96k", "-vbr", "off"];
    case "wav": return [];
    default: throw new FixtureGenerationError("CODEC_UNKNOWN", codec);
  }
};

const OGG_CRC_TABLE = Array.from({ length: 256 }, (_, index) => {
  let value = index << 24;
  for (let bit = 0; bit < 8; bit += 1) value = value & 0x80000000 ? (value << 1) ^ 0x04c11db7 : value << 1;
  return value >>> 0;
});

const normalizeOggPages = (bytes, serial) => {
  let offset = 0;
  while (offset < bytes.length) {
    if (bytes.toString("ascii", offset, offset + 4) !== "OggS") throw new FixtureGenerationError("OGG_PAGE_INVALID");
    const segmentCount = bytes[offset + 26];
    if (segmentCount === undefined) throw new FixtureGenerationError("OGG_PAGE_INVALID");
    let bodySize = 0;
    for (let index = 0; index < segmentCount; index += 1) bodySize += bytes[offset + 27 + index] ?? 0;
    const pageSize = 27 + segmentCount + bodySize;
    bytes.writeUInt32LE(serial, offset + 14);
    bytes.writeUInt32LE(0, offset + 22);
    let crc = 0;
    for (let index = offset; index < offset + pageSize; index += 1) {
      crc = ((crc << 8) ^ OGG_CRC_TABLE[((crc >>> 24) ^ bytes[index]) & 0xff]) >>> 0;
    }
    bytes.writeUInt32LE(crc, offset + 22);
    offset += pageSize;
  }
  return bytes;
};

const transcode = async ({ ffmpegPath, input, output, codec }) => {
  const child = Bun.spawn([
    ffmpegPath, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-fflags", "+bitexact",
    "-i", input, "-map_metadata", "-1", "-threads", "1", ...codecArguments(codec), output,
  ], { stdout: "ignore", stderr: "pipe" });
  const exitCode = await child.exited;
  if (exitCode !== 0) throw new FixtureGenerationError("FFMPEG_FAILED", await new Response(child.stderr).text());
  if (codec === "ogg" || codec === "opus") {
    const bytes = Buffer.from(await readFile(output));
    await Bun.write(output, normalizeOggPages(bytes, codec === "ogg" ? 0x4a53564f : 0x4a534f50));
  }
};

export const generateMediaFixtures = async ({ outputDirectory, ffmpegPath }) => {
  if (!isAbsolute(ffmpegPath)) throw new FixtureGenerationError("FFMPEG_PATH_NOT_ABSOLUTE");
  await mkdir(outputDirectory, { recursive: true });
  const files = [];
  for (const signal of SIGNALS) {
    const wavPath = join(outputDirectory, `${signal.name}.source.wav`);
    await writeWav(wavPath, signal);
    for (const codec of CODECS) {
      const relativePath = `${signal.name}.${codec}`;
      const output = join(outputDirectory, relativePath);
      if (codec === "wav") await copyFile(wavPath, output);
      else await transcode({ ffmpegPath, input: wavPath, output, codec });
      const bytes = await readFile(output);
      files.push({
        signal: signal.name,
        codec,
        relativePath,
        sha256: createHash("sha256").update(bytes).digest("hex"),
        sampleRateHz: SAMPLE_RATE,
        channels: 1,
        durationMs: signal.durationSeconds * 1_000,
      });
    }
    await Bun.file(wavPath).delete();
  }
  const manifest = { schemaVersion: 1, files };
  await Bun.write(join(outputDirectory, "fixture-manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  return manifest;
};

const invokedPath = process.argv[1] === undefined ? undefined : pathToFileURL(process.argv[1]).href;
if (invokedPath === import.meta.url) {
  const outputDirectory = process.argv[2];
  const ffmpegPath = process.argv[3];
  if (outputDirectory === undefined || ffmpegPath === undefined) throw new FixtureGenerationError("ARGUMENTS_REQUIRED");
  process.stdout.write(`${JSON.stringify(await generateMediaFixtures({ outputDirectory, ffmpegPath }))}\n`);
}
