import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Web 控制台构建配置：输出到 dist/，由服务端 embed.FS 嵌入并在 Web 端口提供。
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // 本地开发时把 API/healthz 代理到服务端 Web 端口，避免跨端口 Cookie 问题。
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
});
