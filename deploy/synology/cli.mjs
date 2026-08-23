import { spawn } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import {
  parseFixture,
  redactedContractEvidence,
  validateCompose,
  validateFixture,
  validateSupportMatrix,
} from "./contract.mjs";

const repository = new URL("../../", import.meta.url).pathname;
const args = process.argv.slice(2);
const option = (name, fallback) => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : fallback;
};
const resolve = (path) => {
  if (!path) return undefined;
  return path.startsWith("/") ? path : `${repository}${path}`;
};
const writeJson = async (path, value) => {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
};
const fail = (findings, exitCode = 65) => {
  for (const finding of findings) console.error(finding);
  process.exit(exitCode);
};

const validate = async () => {
  const fixturePath = resolve(option("--fixture"));
  if (!fixturePath) fail(["MISSING_FIXTURE"], 64);
  let fixture;
  try {
    fixture = await parseFixture(fixturePath);
  } catch (error) {
    fail([`INVALID_FIXTURE:${error instanceof Error ? error.message : "unknown"}`]);
  }
  const fixtureResult = validateFixture(fixture);
  if (fixtureResult.exitCode === 78) fail(fixtureResult.findings, 78);
  const composePath = resolve(option("--compose", "deploy/docker/server/compose.synology.yaml"));
  const supportPath = resolve(option("--support-matrix", "deploy/synology/support-matrix.yaml"));
  const composeFindings = validateCompose(await readFile(composePath, "utf8"));
  const matrixFindings = validateSupportMatrix(await readFile(supportPath, "utf8"));
  const findings = [...fixtureResult.findings, ...composeFindings, ...matrixFindings];
  if (findings.length) fail(findings);
  const evidence = redactedContractEvidence(fixture);
  const evidencePath = option("--evidence");
  if (evidencePath) await writeJson(resolve(evidencePath), evidence);
  console.log(JSON.stringify(evidence));
};

const remoteProbe = [
  "set -eu",
  "get=/usr/syno/bin/synogetkeyvalue",
  "model=$($get /etc.defaults/synoinfo.conf upnpmodelname)",
  "version=$($get /etc.defaults/VERSION productversion)",
  "build=$($get /etc.defaults/VERSION buildnumber)",
  "update=$($get /etc.defaults/VERSION smallfixnumber)",
  "package=$($get /var/packages/ContainerManager/INFO package)",
  "arch=$(uname -m)",
  "docker_version=$(/var/packages/ContainerManager/target/usr/bin/docker --version | awk '{print $3}' | tr -d ',')",
  "printf 'model=%s\\nversion=%s\\nbuild=%s\\nupdate=%s\\npackage=%s\\narch=%s\\ndocker=%s\\n' \"$model\" \"$version\" \"$build\" \"$update\" \"$package\" \"$arch\" \"$docker_version\"",
].join("; ");

const collect = (stream) => new Promise((resolveValue) => {
  let output = "";
  stream.setEncoding("utf8");
  stream.on("data", (chunk) => { output += chunk; });
  stream.on("end", () => resolveValue(output));
});

const probeReadonly = async () => {
  const hostEnvironment = option("--host-env");
  const host = hostEnvironment ? process.env[hostEnvironment] : undefined;
  const outputPath = option("--output");
  if (!hostEnvironment || !host) fail(["MISSING_HOST_ENV"], 64);
  if (!args.includes("--redact")) fail(["REDACTION_REQUIRED"], 64);
  if (!outputPath) fail(["MISSING_OUTPUT"], 64);
  const user = process.env.DSM_USER;
  const target = user ? `${user}@${host}` : host;
  const child = spawn("ssh", [
    "-o", "LogLevel=ERROR",
    "-o", "StrictHostKeyChecking=no",
    "-o", "UserKnownHostsFile=/dev/null",
    "-o", "ConnectTimeout=10",
    target,
    remoteProbe,
  ], { stdio: ["inherit", "pipe", "pipe"] });
  const [status, stdout] = await Promise.all([
    new Promise((resolveValue) => child.on("close", resolveValue)),
    collect(child.stdout),
    collect(child.stderr),
  ]);
  if (status !== 0) fail(["DSM_READONLY_PROBE_FAILED"], 69);
  const fields = Object.fromEntries(stdout.trim().split("\n").map((line) => line.split("=", 2)));
  const dsmVersion = `${fields.version}-${fields.build} Update ${fields.update}`;
  const findings = [];
  if (fields.model !== "DS918+") findings.push("UNEXPECTED_MODEL");
  if (dsmVersion !== "7.2.2-72806 Update 9") findings.push("UNEXPECTED_DSM_VERSION");
  if (fields.arch !== "x86_64") findings.push("UNEXPECTED_ARCHITECTURE");
  if (fields.package !== "ContainerManager" || fields.docker !== "24.0.2") findings.push("UNEXPECTED_CONTAINER_RUNTIME");
  if (findings.length) fail(findings);
  const evidence = {
    schema_version: 1,
    probe: "redacted-read-only",
    target: {
      model: fields.model,
      dsm_version: dsmVersion,
      architecture: fields.arch,
      container_manager: "Container Manager",
      docker_version: fields.docker,
    },
    host: "REDACTED",
    credentials_retained: false,
    mutation_commands_executed: false,
    runtime_certification: "candidate-pending-runtime-authorization",
  };
  await writeJson(resolve(outputPath), evidence);
  console.log(JSON.stringify(evidence));
};

if (args[0] === "validate") await validate();
else if (args[0] === "probe-readonly") await probeReadonly();
else fail(["usage: synology validate|probe-readonly"], 64);
