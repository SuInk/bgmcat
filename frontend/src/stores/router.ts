import { ref } from "vue";

import type { SettingsPageKey } from "../components/settings/context";

export type TabKey =
  | "home"
  | "manga"
  | "favorites"
  | "history"
  | "subscriptions"
  | "downloads"
  | "files"
  | "logs"
  | "settings";

export const TABS: Array<{ key: TabKey; label: string; hint: string; icon: string; subtitle: string }> = [
  { key: "home", label: "番剧发现", hint: "Bangumi", icon: "🌸", subtitle: "新番时间表与全库番剧索引" },
  { key: "manga", label: "漫画阅读", hint: "Manga", icon: "📖", subtitle: "在线漫画检索与沉浸式阅读" },
  { key: "favorites", label: "我的收藏", hint: "Watch", icon: "⭐", subtitle: "收藏的番剧与追番订阅" },
  { key: "history", label: "观看历史", hint: "History", icon: "🕒", subtitle: "本地与云盘播放进度及观看记录" },
  { key: "subscriptions", label: "订阅配置", hint: "RSS", icon: "📡", subtitle: "RSS 源与自动下载规则" },
  { key: "downloads", label: "下载管理", hint: "Downloads", icon: "📥", subtitle: "下载任务与边下边播" },
  { key: "files", label: "文件管理", hint: "Files", icon: "📁", subtitle: "本地下载目录与云盘浏览播放" },
  { key: "logs", label: "运行日志", hint: "Logs", icon: "📋", subtitle: "后端服务运行记录" },
  { key: "settings", label: "设置", hint: "服务", icon: "⚙️", subtitle: "服务、下载、数据源与 MCP 接入" }
];

const TAB_PATH_BY_KEY: Record<TabKey, string> = {
  home: "/",
  manga: "/manga",
  favorites: "/watch",
  history: "/history",
  subscriptions: "/rss",
  downloads: "/downloads",
  files: "/files",
  logs: "/logs",
  settings: "/service"
};

// 旧版路径别名，保持既有书签/外链可用
const LEGACY_PATH_TABS: Array<[string, TabKey]> = [
  ["/bangumi", "home"],
  ["/aria2", "downloads"],
  ["/status", "settings"],
  ["/probe", "settings"],
  ["/mcp", "settings"],
  ["/feed", "subscriptions"]
];

/** 从旧路径（如 /probe）或其他视图跳进设置页时，指定直接打开的设置子分类 */
export const requestedSettingsPage = ref<SettingsPageKey | null>(null);

export const activeTab = ref<TabKey>(resolveTabFromPath(window.location.pathname));

/** 设置页接管整个窗口，「返回应用」回到进设置之前的那一页 */
export const lastAppTab = ref<TabKey>(activeTab.value === "settings" ? "home" : activeTab.value);

/** downloads 页在线播放时，把标题写进路径便于分享/恢复 */
export const playbackRouteLabel = ref("");

export function resolveTabFromPath(pathname: string): TabKey {
  const normalizedPath = pathname.replace(/\/+$/g, "") || "/";
  if (normalizedPath === "/probe" || normalizedPath.startsWith("/probe/")) {
    requestedSettingsPage.value = "probe";
    try {
      window.localStorage.setItem("bgmcat.settings.page", "probe");
    } catch {
      // 忽略无痕或受限环境下的存储异常
    }
  } else if (normalizedPath === "/mcp" || normalizedPath.startsWith("/mcp/")) {
    requestedSettingsPage.value = "mcp";
    try {
      window.localStorage.setItem("bgmcat.settings.page", "mcp");
    } catch {
      // 忽略无痕或受限环境下的存储异常
    }
  } else if (normalizedPath === "/service") {
    const params = new URLSearchParams(window.location.search);
    if (params.has("cloud") || params.has("cloudError")) {
      requestedSettingsPage.value = "download";
      try {
        window.localStorage.setItem("bgmcat.settings.page", "download");
      } catch {
        // 忽略无痕或受限环境下的存储异常
      }
    }
  }
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

/** OAuth 回调把一次性兑换码放在 `?login=`，换到 token 后立刻从地址栏抹掉。 */
export function readLoginCodeFromLocation(): string {
  return new URLSearchParams(window.location.search).get("login")?.trim() ?? "";
}

export function hasLoginCodeInLocation(): boolean {
  return new URLSearchParams(window.location.search).has("login");
}

export function readAuthErrorFromLocation(): string {
  return new URLSearchParams(window.location.search).get("authError")?.trim() ?? "";
}

export function hasAuthErrorInLocation(): boolean {
  return new URLSearchParams(window.location.search).has("authError");
}

/** 读取扫码预填参数（优先读取不进网络请求的 #u=...&p=...，也兼容 ?u=...）。 */
export function readAutofillFromLocation(): { username: string; password: string; hadAutofill: boolean } {
  const searchParams = new URLSearchParams(window.location.search);
  const hashRaw = window.location.hash.replace(/^#/, "");
  const hashParams = new URLSearchParams(hashRaw);
  const username = (hashParams.get("u") ?? searchParams.get("u") ?? "").trim();
  const password = hashParams.get("p") ?? "";
  const hadAutofill = searchParams.has("u") || hashParams.has("u") || hashParams.has("p");
  return { username, password, hadAutofill };
}

export function readPlaybackGidFromLocation(): string {
  return new URLSearchParams(window.location.search).get("playback")?.trim() ?? "";
}

export function readAnimeIdFromLocation(): string {
  return new URLSearchParams(window.location.search).get("anime")?.trim() ?? "";
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
  options: { clearToken?: boolean; playbackGid?: string | null; animeId?: string | null } = {}
): string {
  const searchParams = new URLSearchParams(window.location.search);
  if (options.clearToken) {
    searchParams.delete("token");
    searchParams.delete("login");
    searchParams.delete("authError");
    searchParams.delete("u");
  }
  if (options.playbackGid !== undefined) {
    if (options.playbackGid) {
      searchParams.set("playback", options.playbackGid);
    } else {
      searchParams.delete("playback");
    }
  }
  if (options.animeId !== undefined) {
    if (options.animeId) {
      searchParams.set("anime", options.animeId);
    } else {
      searchParams.delete("anime");
    }
  }
  const search = searchParams.toString();
  const hashParams = new URLSearchParams(window.location.hash.replace(/^#/, ""));
  const preserveHash = !(options.clearToken && (hashParams.has("u") || hashParams.has("p")));
  const hash = preserveHash ? window.location.hash : "";
  return `${pathname}${search ? `?${search}` : ""}${hash}`;
}

export function syncLocationWithTab(
  mode: "push" | "replace" = "replace",
  options: { clearToken?: boolean; playbackGid?: string | null; animeId?: string | null } = {}
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
