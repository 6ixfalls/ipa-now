import { writeFileSync } from "node:fs";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";
export default defineConfig({
  plugins: [
    react(),
    {
      name: "keep-embed-placeholder",
      closeBundle() {
        writeFileSync(
          new URL("../internal/web/dist/.gitkeep", import.meta.url),
          "",
        );
      },
    },
  ],
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    cors: false,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
        configure(proxy) {
          proxy.on("proxyReq", (req) => {
            const origin = req.getHeader("origin");
            if (origin === "http://127.0.0.1:5173")
              req.setHeader("origin", "http://127.0.0.1:8080");
          });
        },
      },
    },
  },
  build: { outDir: "../internal/web/dist", emptyOutDir: true },
  test: { environment: "jsdom", restoreMocks: true },
});
