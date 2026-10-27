import { createApp } from "vue";

import App from "./App.vue";
import { readDesktopShellPlatform } from "./api";
import "./style.css";

// 桌面版 macOS 去掉了系统标题栏，红绿灯画在页面顶栏里：样式靠这个属性给它们留位置、开拖拽区
if (readDesktopShellPlatform() === "darwin") {
  document.documentElement.dataset.shell = "mac";
}

createApp(App).mount("#app");

