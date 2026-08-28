import { createHash, createHmac, randomUUID } from "node:crypto";
import { mkdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import type { PublicationReceipt } from "./publication-types";

export type UnsignedPublicationReceipt = Omit<PublicationReceipt, "authentication">;

export const writeAuthenticatedReceipt = (input: Readonly<{ readonly path: string; readonly key: Buffer; readonly receipt: UnsignedPublicationReceipt }>): PublicationReceipt => {
  const payload = Buffer.from(JSON.stringify(input.receipt));
  const keyId = createHash("sha256").update(input.key).digest("hex");
  const receipt: PublicationReceipt = {
    ...input.receipt,
    authentication: {
      algorithm: "HMAC-SHA256",
      keyId,
      payloadSha256: createHash("sha256").update(payload).digest("hex"),
      signature: createHmac("sha256", input.key).update(payload).digest("hex"),
    },
  };
  mkdirSync(dirname(input.path), { recursive: true });
  const temporary = `${input.path}.${randomUUID()}.tmp`;
  try {
    writeFileSync(temporary, `${JSON.stringify(receipt, null, 2)}\n`, { flag: "wx", mode: 0o600 });
    renameSync(temporary, input.path);
  } finally {
    rmSync(temporary, { force: true });
  }
  return receipt;
};

export const verifyReceiptAuthentication = (receipt: PublicationReceipt, key: Buffer): boolean => {
  const { authentication: _authentication, ...unsigned } = receipt;
  const payload = Buffer.from(JSON.stringify(unsigned));
  return receipt.authentication.algorithm === "HMAC-SHA256"
    && receipt.authentication.keyId === createHash("sha256").update(key).digest("hex")
    && receipt.authentication.payloadSha256 === createHash("sha256").update(payload).digest("hex")
    && receipt.authentication.signature === createHmac("sha256", key).update(payload).digest("hex");
};
