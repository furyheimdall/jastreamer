import { CompatibilityError } from "./parser";
import type { Cell } from "./parser";

type Pair = {
  readonly base: string;
  readonly subject: string;
  readonly peer: string;
  readonly wire: string;
  readonly capability: string;
};

const pairs: readonly Pair[] = [
  {
    base: "control-candidate-server-old",
    subject: "control-current",
    peer: "server-old",
    wire: "control-v2",
    capability: "control-api",
  },
  {
    base: "control-candidate-server-new",
    subject: "control-current",
    peer: "server-new",
    wire: "control-v3",
    capability: "control-api",
  },
  {
    base: "renderer-candidate-server-old",
    subject: "renderer-candidate",
    peer: "server-old",
    wire: "renderer-v2",
    capability: "render",
  },
  {
    base: "renderer-candidate-server-new",
    subject: "renderer-candidate",
    peer: "server-new",
    wire: "renderer-v3",
    capability: "render",
  },
  {
    base: "server-candidate-control-old",
    subject: "server-new",
    peer: "control-old",
    wire: "control-v2",
    capability: "control-api",
  },
  {
    base: "server-candidate-control-current",
    subject: "server-new",
    peer: "control-current",
    wire: "control-v3",
    capability: "control-api",
  },
  {
    base: "server-candidate-renderer-old",
    subject: "server-new",
    peer: "renderer-old",
    wire: "renderer-v2",
    capability: "render",
  },
  {
    base: "server-candidate-renderer-current",
    subject: "server-new",
    peer: "renderer-candidate",
    wire: "renderer-v3",
    capability: "render",
  },
];

const orders: readonly Cell["order"][] = [
  "old-first",
  "new-first",
];

const expected = new Map(
  pairs.flatMap((pair) =>
    orders.map((order) => [
      `${pair.base}-${order}`,
      { ...pair, order },
    ]),
  ),
);

export const validateCells = (cells: readonly Cell[]): void => {
  if (cells.length !== expected.size)
    throw new CompatibilityError("exact 16-cell envelope required");
  const actual = new Map(cells.map((cell) => [cell.id, cell]));
  if (actual.size !== expected.size)
    throw new CompatibilityError("duplicate compatibility cell id");
  for (const [id, specification] of expected) {
    const cell = actual.get(id);
    if (
      !cell ||
      cell.subject !== specification.subject ||
      cell.peer !== specification.peer ||
      cell.order !== specification.order ||
      cell.wirePayload !== specification.wire ||
      cell.requiredCapability !== specification.capability
    )
      throw new CompatibilityError(`compatibility cell mismatch: ${id}`);
  }
};
