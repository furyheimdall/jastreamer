import { expect, test } from "bun:test";
import { serverSourceInputs } from "../tooling/identity";

test("Server source identity covers delegated candidate workflows", () => {
  expect(serverSourceInputs).toEqual(
    expect.arrayContaining([
      ".github/workflows/server-qualification-platforms.yml",
      ".github/workflows/server-qualification-windows.yml",
      ".github/workflows/server-qualification-stage.yml",
    ]),
  );
});
