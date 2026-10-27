import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

const backend = "http://127.0.0.1:3001";

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: { alias: { "@shared": fileURLToPath(new URL("../shared", import.meta.url)) } },
  build: { outDir: "../web/dist", emptyOutDir: true },
  server: {
    host: "127.0.0.1",
    proxy: {
      // changeOrigin 必须关掉：后端要求 Origin 与 Host 同源才放行写请求。Vite 的字符串简写默认
      // changeOrigin: true，会把 Host 改成 127.0.0.1:3001，而浏览器的 Origin 还是 localhost:5173，
      // 结果开发环境里所有保存、订阅之类的写操作都被当成跨站请求拒掉。
      "/api": { target: backend, changeOrigin: false },
      "/mcp": {
        target: backend,
        changeOrigin: false,
        // `/mcp` 也是前端的 MCP 页面路由。浏览器直接打开或刷新这一页时别转给后端，
        // 否则拿回的是后端内嵌的生产版 index.html，引用的打包资源开发服务器里没有，页面一片空白。
        bypass(req) {
          if (req.method === "GET" && req.url === "/mcp" && req.headers.accept?.includes("text/html")) {
            return "/index.html";
          }
        }
      }
    }
  }
});
