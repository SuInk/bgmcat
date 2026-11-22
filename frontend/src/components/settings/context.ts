import { ref, type InjectionKey } from "vue";

export type SettingsPageKey =
  | "general"
  | "data"
  | "about"
  | "download"
  | "cloud"
  | "network"
  | "probe"
  | "sources"
  | "metadata"
  | "ai"
  | "mcp"
  | "remote"
  | "tunnel"
  | "github";

export function normalizeSettingsPageKey(key: SettingsPageKey | string | null | undefined): SettingsPageKey {
  switch (key) {
    case "cloud":
      return "download";
    case "probe":
    case "network":
      return "network";
    case "metadata":
      return "sources";
    case "github":
      return "remote";
    case "integrations":
      return "mcp";
    case "general":
    case "data":
    case "about":
    case "download":
    case "sources":
    case "ai":
    case "mcp":
    case "remote":
    case "tunnel":
      return key;
    default:
      return "general";
  }
}

/** 跳转到「网络直连」时预选的目标 ID 或域名（如 "google" / "mikan" / "garden.onekuma.cn"） */
export const requestedProbeTarget = ref<{ id: string; host?: string; name?: string } | null>(null);

/** 兼容旧引用 */
export const probeDetailsOpenSignal = ref(0);

/**
 * 各分类页都同时挂载（搜索要跨页过滤 DOM），只是用 v-show 藏起来。
 * 拉网络的重组件（版本面板、直连探测）靠这个判断自己当前是否真的露出来了，避免一进设置就全量请求。
 */
export const settingsPageVisibleKey: InjectionKey<(key: SettingsPageKey) => boolean> = Symbol("settings-page-visible");

/** 子分类页跳转到其它设置子分类 */
export const settingsOpenPageKey: InjectionKey<(key: SettingsPageKey) => void> = Symbol("settings-open-page");

/** 从任意子分类页一键打开「网络直连」并选中指定目标（若传入自定义域名则自动创建或匹配） */
export const settingsOpenProbeKey: InjectionKey<(targetId: string, host?: string, name?: string) => void> =
  Symbol("settings-open-probe");
