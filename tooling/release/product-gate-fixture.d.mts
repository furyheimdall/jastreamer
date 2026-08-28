export type ProductGateFixture = {
  readonly receiptPath: string;
  readonly trustConfigPath: string;
  readonly mutationLedgerPath: string;
  readonly publicationReceiptKey: Buffer;
};

export function createPromotionFixture(root: string, recordedAt: string, observedInventory?: unknown): Promise<ProductGateFixture>;
