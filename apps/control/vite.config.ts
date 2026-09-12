import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: fileURLToPath(new URL("../server/web/ui/dist", import.meta.url)),
    emptyOutDir: true,
    sourcemap: false,
  },
});
