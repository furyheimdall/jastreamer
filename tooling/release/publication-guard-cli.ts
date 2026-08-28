import { guardPublication, type GuardOptions } from "./publication-guard";

const valueArguments = new Set([
  "--component",
  "--event",
  "--manifest",
  "--stage-root",
  "--output",
  "--product-gate-receipt",
  "--verified-product-gate",
  "--expected-product-gate-sha256",
]);

function parseArguments(args: readonly string[]): GuardOptions {
  const values = new Map<string, string>();
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index];
    const value = args[index + 1];
    if (name === undefined || value === undefined || !valueArguments.has(name) || values.has(name)) {
      throw new TypeError("PUBLICATION_GUARD_USAGE");
    }
    values.set(name, value);
  }
  const componentValue = values.get("--component");
  const eventValue = values.get("--event");
  const component = componentValue === "server" || componentValue === "control" || componentValue === "renderer" ? componentValue : undefined;
  const event = eventValue === "push" || eventValue === "workflow_dispatch" ? eventValue : undefined;
  const manifestPath = values.get("--manifest");
  const outputPath = values.get("--output");
  const stageRoot = values.get("--stage-root");
  if (component === undefined || event === undefined || manifestPath === undefined || outputPath === undefined) {
    throw new TypeError("PUBLICATION_GUARD_USAGE");
  }
  const productGateReceiptPath = values.get("--product-gate-receipt");
  const verifiedProductGatePath = values.get("--verified-product-gate");
  const expectedProductGateSha256 = values.get("--expected-product-gate-sha256");
  return {
    component,
    event,
    manifestPath,
    outputPath,
    ...(stageRoot === undefined ? {} : { stageRoot }),
    ...(productGateReceiptPath === undefined ? {} : { productGateReceiptPath }),
    ...(verifiedProductGatePath === undefined ? {} : { verifiedProductGatePath }),
    ...(expectedProductGateSha256 === undefined ? {} : { expectedProductGateSha256 }),
  };
}

try {
  const result = guardPublication(parseArguments(process.argv.slice(2)));
  if (!result.ok) {
    console.error(result.code);
    process.exit(65);
  }
  console.log(JSON.stringify(result.receipt));
} catch (error) {
  // no-excuse-ok: catch -- CLI boundary serializes typed and built-in input errors.
  if (error instanceof Error) {
    console.error(error.message);
    process.exit(64);
  }
  throw error;
}
