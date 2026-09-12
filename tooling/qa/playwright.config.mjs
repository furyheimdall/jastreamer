import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { defineConfig } = require("../../apps/control/node_modules/@playwright/test");

export default defineConfig({
  testDir: ".",
  testMatch: "web-smoke.spec.mjs",
  workers: 1,
  timeout: 30_000,
  use: {
    headless: true,
    viewport: { width: 1440, height: 900 },
    launchOptions: process.env.JASTREAMER_CHROMIUM
      ? { executablePath: process.env.JASTREAMER_CHROMIUM }
      : {},
  },
});
