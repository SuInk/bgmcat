import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
export default defineConfig({ plugins: [vue(), tailwindcss()], resolve: { alias: { "@shared": fileURLToPath(new URL("../shared", import.meta.url)) } }, build: { outDir: "../web/dist", emptyOutDir: true }, server: { host: "127.0.0.1", proxy: { "/api": "http://127.0.0.1:3001", "/mcp": "http://127.0.0.1:3001" } } });
