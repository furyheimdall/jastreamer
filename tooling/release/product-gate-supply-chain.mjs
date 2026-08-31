import { createPublicKey, verify } from "node:crypto";
import Ajv2020 from "../qa/node_modules/ajv/dist/2020.js";
import schema from "./product-gate-supply-chain.schema.json" with { type: "json" };
import { registerSchemaFormats } from "../qa/schema-validation.mjs";

const ajv = registerSchemaFormats(new Ajv2020({ allErrors: true, strict: false })); ajv.addFormat("uri", (value) => { try { new URL(value); return true; } catch { return false; } });
const validateSpdx = ajv.compile(schema.$defs.spdx); const validateSlsa = ajv.compile(schema.$defs.slsa);

const pae = (type, payload) => Buffer.concat([Buffer.from(`DSSEv1 ${Buffer.byteLength(type)} ${type} ${payload.length} `), payload]);

const artifactTrust = (context, keyId, tools) => {
  const item = context.trust.config.artifactSigning.publicKeys.find((entry) => entry.keyId === keyId);
  if (item === undefined) return undefined;
  const read = tools.stableRead(context.root, item.path);
  return read.issue ? undefined : createPublicKey(read.bytes);
};

export const verifyDsse = (context, evidence, tools) => {
  const { envelope, digest, predicateType, subjectName } = evidence;
  if (envelope?.payloadType !== "application/vnd.in-toto+json" || !Array.isArray(envelope.signatures) || envelope.signatures.length !== 1) return tools.denied("DSSE_INVALID", "payloadType");
  const payload = Buffer.from(envelope.payload, "base64");
  const signature = envelope.signatures[0];
  const key = artifactTrust(context, signature.keyid, tools);
  if (key === undefined || !verify(null, pae(envelope.payloadType, payload), key, Buffer.from(signature.sig, "base64"))) return tools.denied("ATTESTATION_SIGNATURE_INVALID", "signatures");
  const statement = JSON.parse(payload);
  if (statement._type !== "https://in-toto.io/Statement/v1" || statement.predicateType !== predicateType || statement.subject?.length !== 1 || statement.subject[0]?.digest?.sha256 !== digest || (subjectName !== undefined && statement.subject[0]?.name !== subjectName)) return tools.denied("ATTESTATION_SEMANTICS_INVALID", "statement");
  return { statement };
};

const exactKeys = (value, expected) => value !== null && typeof value === "object" && JSON.stringify(Object.keys(value).sort()) === JSON.stringify([...expected].sort());
const validSpdx = (value, artifact, context) => {
  const packageItem = value?.packages?.[0]; const file = value?.files?.[0]; const relationships = value?.relationships;
  return validateSpdx(value) && exactKeys(value, ["spdxVersion", "dataLicense", "SPDXID", "name", "documentNamespace", "creationInfo", "packages", "files", "relationships"]) && exactKeys(value.creationInfo, ["created", "creators"]) && exactKeys(packageItem, ["name", "SPDXID", "downloadLocation", "filesAnalyzed", "licenseConcluded", "licenseDeclared", "copyrightText", "checksums"]) && exactKeys(file, ["fileName", "SPDXID", "checksums", "licenseConcluded", "copyrightText"]) && value?.spdxVersion === "SPDX-2.3" && value.dataLicense === "CC0-1.0" && value.SPDXID === "SPDXRef-DOCUMENT" && value.name === `${artifact.kind} SBOM` && value.documentNamespace === `https://jastreamer.invalid/spdx/${artifact.sha256}`
    && value.creationInfo?.created === context.receipt.recordedAt && JSON.stringify(value.creationInfo?.creators) === JSON.stringify(["Tool: jastreamer-product-gate"])
    && value.packages?.length === 1 && packageItem.name === artifact.kind && packageItem.SPDXID === "SPDXRef-Package" && packageItem.downloadLocation === "NOASSERTION" && packageItem.filesAnalyzed === true && packageItem.licenseConcluded === "Apache-2.0" && packageItem.licenseDeclared === "Apache-2.0" && packageItem.copyrightText === "NOASSERTION"
    && packageItem.checksums?.length === 1 && packageItem.checksums[0].algorithm === "SHA256" && packageItem.checksums[0].checksumValue === artifact.sha256
    && value.files?.length === 1 && file.fileName === artifact.path && file.SPDXID === "SPDXRef-File" && file.licenseConcluded === "Apache-2.0" && file.copyrightText === "NOASSERTION" && file.checksums?.length === 1 && file.checksums[0].algorithm === "SHA256" && file.checksums[0].checksumValue === artifact.sha256
    && relationships?.length === 2 && relationships.every((item) => exactKeys(item, ["spdxElementId", "relationshipType", "relatedSpdxElement"])) && relationships.some((item) => item.spdxElementId === "SPDXRef-DOCUMENT" && item.relationshipType === "DESCRIBES" && item.relatedSpdxElement === "SPDXRef-Package") && relationships.some((item) => item.spdxElementId === "SPDXRef-Package" && item.relationshipType === "CONTAINS" && item.relatedSpdxElement === "SPDXRef-File");
};

export const verifySupplyChain = (context, artifacts, tools) => {
  const security = tools.readAuthenticated(context, context.receipt.supplyChain.security);
  if (security.issue) return security.issue;
  if (!tools.validateSecuritySchema(security.value) || !tools.identityMatches(security.value, context.receipt)) return tools.denied("SECURITY_GATE_FAILED", "supplyChain.security");
  for (const lane of ["sbom", "provenance", "signing"]) if (context.receipt.supplyChain[lane].length !== artifacts.length) return tools.denied("SUPPLY_CHAIN_INCOMPLETE", `supplyChain.${lane}`);
  for (const [index, artifact] of artifacts.entries()) {
    const sbom = tools.readAuthenticated(context, context.receipt.supplyChain.sbom[index]); if (sbom.issue) return sbom.issue;
    if (!validSpdx(sbom.value, artifact, context)) return tools.denied("SPDX_INVALID", sbom.value?.name ?? "sbom");
    const provenance = tools.readAuthenticated(context, context.receipt.supplyChain.provenance[index]); if (provenance.issue) return provenance.issue;
    const attestation = verifyDsse(context, { envelope: provenance.value, digest: artifact.sha256, predicateType: "https://slsa.dev/provenance/v1", subjectName: artifact.kind }, tools); if (attestation?.ok === false) return attestation;
    if (!validateSlsa(attestation.statement)) return tools.denied("SLSA_INVALID", "provenance.schema");
    const definition = attestation.statement.predicate?.buildDefinition; const dependencies = definition?.resolvedDependencies; const builder = attestation.statement.predicate?.runDetails?.builder?.id;
    const expectedPlatforms = artifact.kind === "server-oci" ? ["linux/amd64", "linux/arm64"] : [];
    if (!exactKeys(definition, ["buildType", "externalParameters", "resolvedDependencies"]) || !exactKeys(definition?.externalParameters, ["artifactKind", "platforms"]) || !exactKeys(attestation.statement.predicate?.runDetails, ["builder"]) || !exactKeys(dependencies?.[0], ["uri", "digest"]) || !exactKeys(dependencies?.[0]?.digest, ["gitCommit"]) || !exactKeys(dependencies?.[1], ["uri", "digest"]) || !exactKeys(dependencies?.[1]?.digest, ["sha256"]) || definition?.buildType !== context.trust.config.materialPolicy.buildType || definition.externalParameters?.artifactKind !== artifact.kind || JSON.stringify(definition.externalParameters?.platforms) !== JSON.stringify(expectedPlatforms) || dependencies?.length !== 2 || dependencies[0]?.uri !== context.trust.config.materialPolicy.sourceUri || dependencies[0]?.digest?.gitCommit !== context.receipt.source.revision || dependencies[1]?.uri !== `${context.trust.config.materialPolicy.sourceUri}#source-input` || dependencies[1]?.digest?.sha256 !== context.receipt.source.dirtySha256 || !context.trust.config.builders.includes(builder)) return tools.denied("SLSA_INVALID", "provenance");
    const signing = tools.readAuthenticated(context, context.receipt.supplyChain.signing[index]); if (signing.issue) return signing.issue;
    const key = artifactTrust(context, signing.value?.keyId, tools);
    if (signing.value?.kind !== "artifact_signature" || signing.value.subjectSha256 !== artifact.sha256 || key === undefined || !verify(null, Buffer.from(artifact.sha256), key, Buffer.from(signing.value.signature, "base64"))) return tools.denied("ARTIFACT_SIGNATURE_INVALID", artifact.kind);
  }
};
