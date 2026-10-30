import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 开发态：API 请求代理到 Go 服务（ocr-server，默认 8899）
const target = process.env.OCR_API ?? "http://127.0.0.1:8899";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5273,
    // 固定绑 IPv4 回环：Windows 上 node 的 localhost 会绑到 [::1]，
    // 结果 http://127.0.0.1:5273 打不开（dev 脚本打印的就是这个地址）
    host: "127.0.0.1",
    // 端口被占用时直接报错，而不是悄悄换到 5274
    strictPort: true,
    proxy: {
      "/api": { target, changeOrigin: true },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
