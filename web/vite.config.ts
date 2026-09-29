import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// La API corre en :8080. Proxyamos /v1 y /public para que la cookie de sesión
// sea same-origin en desarrollo (no hay CORS ni SameSite que pelear).
const apiTarget = process.env.RAMPA_API_URL ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/v1": { target: apiTarget, changeOrigin: false },
      "/public": { target: apiTarget, changeOrigin: false },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});
