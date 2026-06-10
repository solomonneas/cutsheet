import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server proxies API calls to a locally running `cutsheet serve`
// (default listen 127.0.0.1:8633), so the UI dev loop needs no CORS setup.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8633",
      "/healthz": "http://127.0.0.1:8633",
    },
  },
});
