import type { InjectionKey } from "vue";

export type SettingsPageKey = "general" | "data" | "about" | "download" | "cloud" | "sources" | "metadata" | "ai" | "remote" | "tunnel" | "github";

/**
 * 各分类页都同时挂载（搜索要跨页过滤 DOM），只是用 v-show 藏起来。
 * 拉网络的重组件（版本面板）靠这个判断自己当前是否真的露出来了，避免一进设置就全量请求。
 */
export const settingsPageVisibleKey: InjectionKey<(key: SettingsPageKey) => boolean> = Symbol("settings-page-visible");
