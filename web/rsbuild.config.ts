import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: { title: "Invoice · 发票工作台", template: "./index.html" },
  output: { assetPrefix: "/invoice/", distPath: { root: "dist" }, cleanDistPath: true },
  server: {
    port: 3002,
    base: "/invoice/",
    proxy: {
      "/invoice-api": "http://127.0.0.1:3001",
      "/api": {
        target: process.env.NEW_API_DEV_URL || "http://127.0.0.1:3000",
        changeOrigin: true,
      },
    },
  },
});
