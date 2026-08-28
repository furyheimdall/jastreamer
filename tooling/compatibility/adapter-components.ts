import { resolve } from "node:path";
import {
  checked,
  flutterImage,
  jsonOutput,
  run,
} from "./adapter-process";
import type {
  CandidateBuilds,
  ComponentEvidence,
} from "./adapter-process";
import {
  CompatibilityError,
  isRecord,
  stringList,
} from "./parser";
import type { Artifact, Cell } from "./parser";

export const executeControl = (
  builds: CandidateBuilds,
  cell: Cell,
  peer: Artifact,
  wireFile: string,
  expectedMajor: number | undefined,
): ComponentEvidence => {
  const command = [
    "docker",
    "run",
    "--rm",
    "--platform",
    "linux/amd64",
    "-v",
    `${builds.controlRoot}:/workspace`,
    "-v",
    `${builds.fixtureRoot}:/fixtures:ro`,
    "-w",
    "/workspace",
    flutterImage,
    "/workspace/.compat/control-adapter",
  ];
  const orderAssertions: string[] = [];
  if (cell.order === "new-first") {
    const disconnected = run([
      ...command,
      "--server-majors",
      "99",
    ]);
    if (
      disconnected.exitCode !== 65 ||
      !disconnected.stderr.includes("UNSUPPORTED_PROTOCOL_MAJOR")
    )
      throw new CompatibilityError(
        "Control disconnected-start assertion failed",
      );
    orderAssertions.push("disconnected-start-safe");
  }
  const result = checked([
    ...command,
    "--fixture",
    `/fixtures/${wireFile}`,
    "--server-majors",
    peer.supportedMajors.join(","),
  ]);
  const value = jsonOutput(result, "Control");
  if (
    expectedMajor === undefined ||
    !isRecord(value) ||
    value.selectedMajor !== expectedMajor ||
    !Array.isArray(value.unknownCapabilities) ||
    !value.unknownCapabilities.includes("future-capability") ||
    !isRecord(value.commandKind) ||
    value.commandKind.state !== "unknown" ||
    (expectedMajor === 2 && peer.capabilities.includes("catalog-browse"))
  )
    throw new CompatibilityError("Control adapter assertion failed");
  return {
    status: "passed",
    trace: ["control-adapter-executed"],
    assertions: [
      "selected-major",
      "unknown-capability-preserved",
      "unknown-enum-preserved",
      ...orderAssertions,
    ],
  };
};

export const executeRenderer = (
  builds: CandidateBuilds,
  cell: Cell,
  peer: Artifact,
  wireFile: string,
  expectedMajor: number | undefined,
): ComponentEvidence => {
  const command = [
    builds.rendererBinary,
    "--compatibility-fixture",
    resolve(builds.fixtureRoot, wireFile),
    "--remote-majors",
    peer.supportedMajors.join(","),
    "--remote-capabilities",
    peer.capabilities.join(","),
  ];
  const orderAssertions: string[] = [];
  if (cell.order === "new-first") {
    const disconnected = run([
      builds.rendererBinary,
      "--compatibility-fixture",
      resolve(builds.fixtureRoot, wireFile),
      "--remote-majors",
      "99",
      "--remote-capabilities",
      peer.capabilities.join(","),
    ]);
    if (
      disconnected.exitCode !== 78 ||
      !disconnected.stderr.includes("UNSUPPORTED_PROTOCOL_MAJOR")
    )
      throw new CompatibilityError(
        "Renderer disconnected-start assertion failed",
      );
    orderAssertions.push("disconnected-start-safe");
  }
  const result = run(command);
  if (expectedMajor === undefined) {
    if (
      result.exitCode !== 78 ||
      !result.stderr.includes("UNSUPPORTED_PROTOCOL_MAJOR")
    )
      throw new CompatibilityError(
        "Renderer unsupported-major assertion failed",
      );
    return {
      status: "unsupported",
      trace: ["renderer-adapter-rejected-major"],
      assertions: ["typed-unsupported-major"],
    };
  }
  if (result.exitCode !== 0)
    throw new CompatibilityError(
      `Renderer adapter failed: ${result.stderr}`,
    );
  const value = jsonOutput(result, "Renderer");
  if (
    !isRecord(value) ||
    value.negotiatedMajor !== expectedMajor ||
    !isRecord(value.commandKind) ||
    value.commandKind.state !== "unsupported" ||
    value.errorCode !== "UNSUPPORTED_COMMAND" ||
    !stringList(value.capabilities, "Renderer capabilities").includes(
      "future-capability",
    ) ||
    (expectedMajor === 2 && peer.capabilities.includes("renderer-session"))
  )
    throw new CompatibilityError("Renderer adapter assertion failed");
  return {
    status: "passed",
    trace: ["renderer-adapter-executed"],
    assertions: [
      "selected-major",
      "unknown-capability-preserved",
      "unknown-command-typed",
      ...orderAssertions,
    ],
  };
};

export const executeServer = (
  builds: CandidateBuilds,
  cell: Cell,
  subject: Artifact,
  peer: Artifact,
  wireFile: string,
  expectedMajor: number | undefined,
): ComponentEvidence => {
  const peerFixture = peer.id === "control-old"
    ? "control-v2-peer.json"
    : peer.id === "renderer-old"
      ? "renderer-v2-peer.json"
      : `${peer.id}.json`;
  const result = run([
    builds.serverBinary,
    "--peer",
    peer.component,
    "--peer-fixture",
    resolve(builds.fixtureRoot, peerFixture),
    "--wire-fixture",
    resolve(builds.fixtureRoot, wireFile),
    "--start-order",
    cell.order,
    "--server-majors",
    subject.supportedMajors.join(","),
  ]);
  if (expectedMajor === undefined) {
    if (
      result.exitCode !== 78 ||
      !result.stderr.includes("UNSUPPORTED_PROTOCOL_MAJOR")
    )
      throw new CompatibilityError(
        "Server unsupported-major assertion failed",
      );
    return {
      status: "unsupported",
      trace: ["server-adapter-rejected-major"],
      assertions: ["typed-unsupported-major"],
    };
  }
  if (result.exitCode !== 0)
    throw new CompatibilityError(
      `Server adapter failed: ${result.stderr}`,
    );
  const value = jsonOutput(result, "Server");
  if (
    !isRecord(value) ||
    value.protocol_major !== expectedMajor ||
    value.start_order !== cell.order ||
    value.request_status !== "unsupported" ||
    value.request_error_code !== "UNSUPPORTED_COMMAND"
  )
    throw new CompatibilityError("Server adapter assertion failed");
  const steps = stringList(value.steps, "Server steps");
  if (
    steps[0] !==
    (cell.order === "old-first" ? "start-peer" : "start-server")
  )
    throw new CompatibilityError("Server order trace assertion failed");
  return {
    status: "passed",
    trace: ["server-adapter-executed", ...steps],
    assertions: [
      "selected-major",
      "unknown-command-typed",
      "start-order-executed",
    ],
  };
};
