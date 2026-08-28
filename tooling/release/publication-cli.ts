import { preparePublication } from "./publication-contract";
import { PublicationContractError, parsePublicationRequest } from "./publication-parse";
import { ProcessPublicationDriver, ProviderConfigurationError } from "./publication-process";
import { executePublication, PublicationTransactionError } from "./publication-transaction";

class PublicationCliError extends Error {
  readonly name = "PublicationCliError";
  constructor(readonly code: string) {
    super(code);
  }
}

const parseArguments = (values: readonly string[]): string => {
  if (values.length !== 2 || values[0] !== "--request" || values[1] === undefined) throw new PublicationCliError("PUBLICATION_USAGE");
  return values[1];
};

const receiptKey = (): Buffer => {
  const encoded = process.env["PUBLICATION_RECEIPT_HMAC_KEY_B64"];
  if (typeof encoded !== "string" || encoded.length === 0) throw new ProviderConfigurationError("PUBLICATION_RECEIPT_KEY_REQUIRED");
  const key = Buffer.from(encoded, "base64");
  if (key.byteLength < 32 || key.toString("base64").replace(/=+$/, "") !== encoded.replace(/=+$/, "")) throw new ProviderConfigurationError("PUBLICATION_RECEIPT_KEY_INVALID");
  return key;
};

try {
  const request = parsePublicationRequest(parseArguments(process.argv.slice(2)));
  if (request.mode !== "production") throw new PublicationCliError("PRODUCTION_MODE_REQUIRED");
  const key = receiptKey();
  const prepared = preparePublication(request, key);
  const result = await executePublication({ prepared, driver: new ProcessPublicationDriver(request.dockerConfigRoot), receiptKey: key });
  console.log(JSON.stringify({ status: result.status, releaseTag: result.releaseTag, selectedAssets: result.selectedAssets.length }));
} catch (error) {
  // no-excuse-ok: catch -- executable boundary emits typed failure codes without provider secrets.
  if (error instanceof PublicationTransactionError || error instanceof PublicationContractError || error instanceof ProviderConfigurationError || error instanceof PublicationCliError) {
    console.error(error.message);
    process.exit(error instanceof PublicationCliError || error instanceof ProviderConfigurationError ? 64 : 65);
  }
  if (error instanceof Error) {
    console.error(error.message);
    process.exit(70);
  }
  throw error;
}
