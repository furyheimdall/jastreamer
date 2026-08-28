import { createHash } from "node:crypto";

const digest = (value) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const source = { revision: "0123456789abcdef0123456789abcdef01234567", sha256: "1".repeat(64) };
const contracts = { controlSha256: "2".repeat(64), rendererSha256: "3".repeat(64) };
const peers = [
  { component: "server", sha256: "4".repeat(64) },
  { component: "control", sha256: "5".repeat(64) },
  { component: "renderer", sha256: "6".repeat(64) },
];
const artifacts = [
  { component: "server", sha256: "7".repeat(64) },
  { component: "control", sha256: "8".repeat(64) },
  { component: "renderer", sha256: "9".repeat(64) },
];

export const REQUIRED_RECEIPTS = [
  "candidate",
  "server_control_e2e",
  "k17",
  "wasapi",
  "ffmpeg",
  "external_authorization_pending",
  "cleanup",
];

export const createSyntheticBundle = (recordedAt) => {
  const artifactSetSha256 = digest(artifacts);
  const peerSetSha256 = digest(peers);
  const binding = {
    sourceSha256: source.sha256,
    artifactSetSha256,
    controlContractSha256: contracts.controlSha256,
    rendererContractSha256: contracts.rendererSha256,
    peerSetSha256,
  };
  const receipt = (kind, payload, evidenceMode = "contract_fixture") => ({
    schemaVersion: 1,
    kind,
    recordedAt,
    runId: "qa-contract-fixture",
    binding: { ...binding },
    evidenceMode,
    payload,
  });
  return {
    schemaVersion: 1,
    kind: "product_qa_bundle",
    purpose: "validator_fixture",
    recordedAt,
    runId: "qa-contract-fixture",
    source: { ...source },
    contracts: { ...contracts },
    peers: peers.map((peer) => ({ ...peer })),
    requiredReceipts: [...REQUIRED_RECEIPTS],
    receipts: [
      receipt("candidate", { artifactSetSha256, artifacts: artifacts.map((artifact) => ({ ...artifact })) }),
      receipt("server_control_e2e", {
        artifactSha256: artifactSetSha256,
        startupOrders: ["server_first", "control_first"],
        scenarios: [
          { id: "bootstrap", result: "passed" },
          { id: "browse-transport", result: "passed" },
          { id: "revocation", result: "passed" },
        ],
      }),
      receipt("k17", (() => {
        const protocolInfo = [
          "http-get:*:audio/flac:*", "http-get:*:audio/mpeg:*", "http-get:*:audio/ogg:*",
          "http-get:*:audio/wav:*", "http-get:*:audio/L16;rate=44100;channels=2:*",
        ];
        const formats = [["flac", "original"], ["mp3", "original"], ["vorbis", "original"], ["opus", "original"], ["wav", "original"], ["l16-fallback", "l16"]];
        return {
          evidenceSource: "physical", artifactSha256: artifactSetSha256, identitySha256: "a".repeat(64),
          model: "FiiO K17", firmware: 261, runnerLabel: "jastreamer-k17-lab-a",
          protocolInfo, protocolInfoSha256: digest(protocolInfo),
          representations: formats.map(([format, selected], index) => ({ format, advertised: true, selected, audioProofSha256: String(index + 1).repeat(64) })),
          transport: { pause: "passed", seek: "passed", stop: "passed", naturalEndCount: 1 },
          lifecycle: { disappearance: "passed", reappearance: "passed" },
          externalOverride: { observed: true, adopted: false },
          network: { https: "passed", explicitMediaOnlyHttp: "passed", privateNetworkOnly: true, hostileLocationRejected: true, redirectsRejected: true, expiredUrlRejected: true },
          audioProof: { captureSha256: "b".repeat(64), method: "automated_capture", manualListening: false },
          cleanup: { rawIdentityRetained: false, firmwareMutated: false, resourcesReleased: true, processesTerminated: true },
          recordedAt,
        };
      })()),
      receipt("wasapi", {
        endpointIdentitySha256: "b".repeat(64),
        captureSha256: "c".repeat(64),
        sampleRateHz: 48_000,
        channels: 2,
        toneFrequencyHz: 1_000,
        durationMs: 2_000,
        seekMarkersObserved: 3,
      }),
      receipt("ffmpeg", {
        executableSha256: "d".repeat(64),
        version: { major: 6, minor: 1, patch: 1 },
        decoders: ["flac", "mp3", "vorbis", "opus", "pcm_s16le"],
        encoder: "pcm_s16be",
        probeStatus: "passed",
      }),
      receipt("external_authorization_pending", {
        gate: "k17",
        qualificationStatus: "awaiting_external_authorization",
        networkCalls: 0,
        audioMutations: 0,
        publicationEligible: false,
      }, "pending"),
      receipt("cleanup", {
        resourcesReleased: true,
        processesTerminated: true,
        temporaryDirectoriesRemoved: true,
        externalWrites: 0,
      }),
    ],
  };
};
