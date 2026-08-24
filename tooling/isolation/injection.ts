import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { InjectionId } from "./types.ts";

export function installInjection(worktree: string, injection: InjectionId): void {
  switch (injection) {
    case "server-imports-control": {
      const directory = join(worktree, "apps/server/internal/isolation");
      mkdirSync(directory, { recursive: true });
      writeFileSync(join(directory, "todo16_injection_test.go"), `package isolation

import (
  "os"
  "testing"
)

func TestServerCannotReadControlSource(t *testing.T) {
  if _, err := os.Stat("../../../control/lib/main.dart"); err != nil {
    t.Fatalf("injected sibling access failed: %v", err)
  }
}
`);
      return;
    }
  }
}
