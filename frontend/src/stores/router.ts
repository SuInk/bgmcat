import { ref } from "vue";

export type TabKey = "home" | "favorites" | "subscriptions" | "downloads" | "logs" | "settings" | "integrations";

export const TABS: Array<{ key: TabKey; label: string; hint: string; icon: string; subtitle: string }> = [
  { key: "home", label: "首页新番", hint: "Bangumi", icon: "🌸", subtitle: "当季新番与番剧搜索" },
  { key: "favorites", label: "我的收藏", hint: "Watch", icon: "⭐", subtitle: "收藏的番剧与追番订阅" },
  { key: "subscriptions", label: "订阅配置", hint: "RSS", icon: "📡", subtitle: "RSS 源与自动下载规则" },
  { key: "downloads", label: "下载管理", hint: "托管 aria2", icon: "📥", subtitle: "下载任务与在线播放" },
  { key: "logs", label: "运行日志", hint: "Logs", icon: "📋", subtitle: "后端服务运行记录" },
  { key: "integrations", label: "MCP", hint: "Agent", icon: "🔌", subtitle: "AI 客户端接入配置" },
  { key: "settings", label: "其他设置", hint: "服务", icon: "⚙️", subtitle: "服务、下载与数据源设置" }
];

const TAB_PATH_BY_KEY: Record<TabKey, string> = {
  home: "/",
  favorites: "/watch",
  subscriptions: "/rss",
  downloads: "/downloads",
  logs: "/logs",
  settings: "/service",
  integrations: "/mcp"
};

// 旧版路径别名，保持既有书签/外链可用
const LEGACY_PATH_TABS: Array<[string, TabKey]> = [
  ["/bangumi", "home"],
  ["/aria2", "downloads"],
  ["/status", "settings"],
  ["/feed", "subscriptions"]
];

export const activeTab = ref<TabKey>(resolveTabFromPath(window.location.pathname));

/** 设置页接管整个窗口，「返回应用」回到进设置之前的那一页 */
export const lastAppTab = ref<TabKey>(activeTab.value === "settings" ? "home" : activeTab.value);

/** downloads 页在线播放时，把标题写进路径便于分享/恢复 */
export const playbackRouteLabel = ref("");

export function resolveTabFromPath(pathname: string): TabKey {
  const normalizedPath = pathname.replace(/\/+$/g, "") || "/";
  for (const [prefix, tab] of LEGACY_PATH_TABS) {
    if (normalizedPath === prefix || normalizedPath.startsWith(`${prefix}/`)) {
      return tab;
    }
  }
  const matched = (Object.entries(TAB_PATH_BY_KEY) as Array<[TabKey, string]>).find(
    ([, path]) => normalizedPath === path || normalizedPath.startsWith(`${path}/`)
  );
  return matched?.[0] ?? "home";
}

export function syncTabFromLocation(): void {
  activeTab.value = resolveTabFromPath(window.location.pathname);
}

export function readTokenFromLocation(): string {
  return new URLSearchParams(window.location.search).get("token")?.trim() ?? "";
}

export function hasTokenInLocation(): boolean {
  return new URLSearchParams(window.location.search).has("token");
}

/** GitHub 回调把一次性兑换码放在 `?login=`，换到 token 后立刻从地址栏抹掉。 */
export function readLoginCodeFromLocation(): string {
  return new URLSearchParams(window.location.search).get("login")?.trim() ?? "";
}

export function hasLoginCodeInLocation(): boolean {
  return new URLSearchParams(window.location.search).has("login");
}

export function readPlaybackGidFromLocation(): string {
  return new URLSearchParams(window.location.search).get("playback")?.trim() ?? "";
}

function normalizePlaybackRouteLabel(value: string): string {
  return value
    .replace(/[\\/#?%]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function buildTabPath(tab: TabKey): string {
  if (tab !== "downloads") {
    return TAB_PATH_BY_KEY[tab];
  }
  const basePath = TAB_PATH_BY_KEY.downloads;
  const playbackLabel = normalizePlaybackRouteLabel(playbackRouteLabel.value);
  return playbackLabel ? `${basePath}/${encodeURIComponent(playbackLabel)}` : basePath;
}

function buildLocationUrl(
  pathname: string,
  options: { clearToken?: boolean; playbackGid?: string | null } = {}
): string {
  const searchParams = new URLSearchParams(window.location.search);
  if (options.clearToken) {
    searchParams.delete("token");
    searchParams.delete("login");
  }
  if (options.playbackGid !== undefined) {
    if (options.playbackGid) {
      searchParams.set("playback", options.playbackGid);
    } else {
      searchParams.delete("playback");
    }
  }
  const search = searchParams.toString();
  return `${pathname}${search ? `?${search}` : ""}${window.location.hash}`;
}

export function syncLocationWithTab(
  mode: "push" | "replace" = "replace",
  options: { clearToken?: boolean; playbackGid?: string | null } = {}
): void {
  const nextOptions = {
    ...options,
    playbackGid: activeTab.value === "downloads" ? options.playbackGid : null
  };
  const targetUrl = buildLocationUrl(buildTabPath(activeTab.value), nextOptions);
  const currentUrl = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (currentUrl === targetUrl) {
    return;
  }

  const method = mode === "push" ? window.history.pushState : window.history.replaceState;
  method.call(window.history, null, "", targetUrl);
}
