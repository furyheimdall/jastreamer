import { existsSync, readdirSync } from "node:fs";
import { join } from "node:path";
import type { ComponentName, PackageReceipt } from "./types.ts";

export function collectPackageReceipt(component: ComponentName, artifactRoot: string): PackageReceipt {
  switch (component) {
    case "server": {
      const artifact = join(artifactRoot, "jstreamer-server");
      return { artifacts: existsSync(artifact) ? [artifact] : [], platformDeferrals: ["windows-linux-packaging:todo18"] };
    }
    case "renderer": {
      const artifact = join(artifactRoot, "jstreamer-renderer");
      return { artifacts: existsSync(artifact) ? [artifact] : [], platformDeferrals: ["windows-msi:todo20"] };
    }
    case "control": {
      const web = join(artifactRoot, "web");
      const artifacts = existsSync(web) && readdirSync(web).length > 0 ? [web] : [];
      return { artifacts, platformDeferrals: ["android-windows-packaging:todo19"] };
    }
  }
}
