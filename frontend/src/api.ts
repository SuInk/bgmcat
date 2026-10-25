import type {
  AgentDetectionResult,
  AgentDoctorResult,
  AgentHost,
  AgentInstallResult,
  AnimeDetail,
  AnimeSearchResult,
  ApiResponse,
  AppSettings,
  BackupListResult,
  CreateBackupResult,
  DownloadJob,
  DownloadConflictStrategy,
  DownloadPlaybackInfo,
  FeedItem,
  FavoriteAnime,
  HomepageAnime,
  LaunchPlayerResult,
  LocalAiChatMessage,
  LocalAiChatResponse,
  LocalAiStatus,
  AuthSessionSummary,
  GithubLoginResult,
  MikanDownloadItem,
  LogEntry,
  OpenDataDirResult,
  RestoreBackupResult,
  PublicSystemStatus,
  ResourceProviderFetchStatus,
  ResourceProviderSummary,
  Subscription,
  SubscriptionRule,
  SystemStatus,
  DiscoveredTunnel,
  TunnelLogEntry,
  TunnelPreflightIssue,
  TunnelStatusSnapshot,
  UpdatePolicy,
  UpdateReleaseSummary,
  UpdateStatus,
  UpdateVersionInfo
} from "@shared/index";
import { t } from "./i18n";

function normalizeBaseUrl(value: string): string {
  return value.replace(/\/+$/g, "");
}

function resolveApiBaseUrl(): string {
  const configuredBaseUrl = import.meta.env.VITE_API_BASE_URL?.trim();
  if (configuredBaseUrl) {
    return normalizeBaseUrl(configuredBaseUrl);
  }
  return "";
}

const API_BASE_URL = resolveApiBaseUrl();
const STORAGE_KEY = "anime-rss-api-token";
const REQUEST_TIMEOUT_MS = 8000;
const HOMEPAGE_REQUEST_TIMEOUT_MS = 30000;

export class ApiRequestError extends Error {
  constructor(
    message: string,
    readonly code?: string
  ) {
    super(message);
  }
}

interface DesktopShellBridge {
  apiToken?: string;
}

/** The Electron shell injects the token through its preload bridge (see electron/preload.cjs). */
export function readDesktopShellToken(): string {
  const bridge = (window as unknown as { animeRssShell?: DesktopShellBridge }).animeRssShell;
  return typeof bridge?.apiToken === "string" ? bridge.apiToken : "";
}

/**
 * Only our own shell counts: the preload bridge, or the UA marker the shell appends.
 * A generic Electron-based browser must still go through the login screen.
 */
export function isDesktopShellRuntime(): boolean {
  return Boolean(readDesktopShellToken()) || /AnimeRSSShell/i.test(window.navigator.userAgent);
}

export function readApiToken(): string {
  // A token stored by the user (or by a rotation) wins so the session survives a rotate;
  // the shell-injected one is the fallback that keeps the desktop app token-free.
  return window.localStorage.getItem(STORAGE_KEY) || readDesktopShellToken();
}

export function persistApiToken(token: string): void {
  window.localStorage.setItem(STORAGE_KEY, token);
}

export function clearApiToken(): void {
  window.localStorage.removeItem(STORAGE_KEY);
}

async function request<T>(path: string, init?: RequestInit, timeoutMs = REQUEST_TIMEOUT_MS): Promise<T> {
  const token = readApiToken();
  const controller = new AbortController();
  let timedOut = false;
  const timeout = window.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  const abortFromCaller = () => controller.abort();
  init?.signal?.addEventListener("abort", abortFromCaller, { once: true });
  const headers = new Headers(init?.headers);
  headers.set("Content-Type", "application/json");
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }

  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, {
      ...init,
      headers,
      signal: controller.signal
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw new ApiRequestError(
        timedOut ? t("api.requestTimeout") : "请求已取消。",
        timedOut ? "REQUEST_TIMEOUT" : "REQUEST_ABORTED"
      );
    }
    throw new ApiRequestError(
      t("api.networkError"),
      "NETWORK_ERROR"
    );
  } finally {
    window.clearTimeout(timeout);
    init?.signal?.removeEventListener("abort", abortFromCaller);
  }

  let payload: ApiResponse<T>;
  try {
    payload = (await response.json()) as ApiResponse<T>;
  } catch {
    throw new ApiRequestError(t("api.invalidResponse", { status: response.status }), "INVALID_RESPONSE");
  }

  if (!payload.ok) {
    throw new ApiRequestError(normalizeApiErrorMessage(payload.error.message, payload.error.code), payload.error.code);
  }
  return payload.data;
}

function normalizeApiErrorMessage(message: string, code?: string): string {
  switch (code) {
    case "ARIA2_OFFLINE":
      return t("api.aria2Offline");
    case "ARIA2_RPC_TIMEOUT":
      return t("api.aria2Timeout");
    case "ARIA2_RPC_INVALID_RESPONSE":
      return t("api.aria2InvalidResponse");
    case "QBITTORRENT_OFFLINE":
      return t("api.qbittorrentOffline");
    case "QBITTORRENT_AUTH_FAILED":
      return t("api.qbittorrentAuthFailed");
    case "QBITTORRENT_INVALID_RESPONSE":
      return t("api.qbittorrentInvalidResponse");
    case "MIKAN_FETCH_FAILED":
      return t("api.mikanFetchFailed");
    case "BANGUMI_DATA_FETCH_FAILED":
      return t("api.bangumiDataFetchFailed");
    default:
      return message;
  }
}

export async function tokenLogin(token: string): Promise<SystemStatus> {
  return request<SystemStatus>("/api/auth/token-login", {
    method: "POST",
    body: JSON.stringify({ token })
  });
}

export async function getSystemStatus(): Promise<SystemStatus> {
  return request<SystemStatus>("/api/system/status/full");
}

/** Unauthenticated liveness probe; exposes nothing beyond the app version. */
export async function getPublicSystemStatus(): Promise<PublicSystemStatus> {
  return request<PublicSystemStatus>("/api/system/status");
}

/** GitHub 登录入口：整页跳到服务端，由服务端 302 到 GitHub。 */
export function githubLoginUrl(): string {
  return `${API_BASE_URL}/api/auth/github/start`;
}

export async function exchangeGithubLogin(code: string): Promise<GithubLoginResult> {
  return request<GithubLoginResult>("/api/auth/github/exchange", {
    method: "POST",
    body: JSON.stringify({ code })
  });
}

export async function listAuthSessions(): Promise<AuthSessionSummary[]> {
  return request<AuthSessionSummary[]>("/api/auth/sessions");
}

export async function revokeAuthSession(id: number): Promise<{ revoked: boolean }> {
  return request<{ revoked: boolean }>(`/api/auth/sessions/${id}`, { method: "DELETE" });
}

export async function logoutSession(): Promise<{ revoked: boolean }> {
  return request<{ revoked: boolean }>("/api/auth/logout", { method: "POST" });
}

export async function getSettings(): Promise<AppSettings> {
  return request<AppSettings>("/api/settings");
}

export async function rotateApiToken(): Promise<AppSettings> {
  return request<AppSettings>("/api/settings/rotate-token", { method: "POST" });
}

export async function updateSettings(patch: Partial<AppSettings>): Promise<AppSettings> {
  return request<AppSettings>("/api/settings", {
    method: "PUT",
    body: JSON.stringify(patch)
  });
}

export async function listSubscriptions(): Promise<Subscription[]> {
  return request<Subscription[]>("/api/subscriptions");
}

export async function createSubscription(payload: {
  name: string;
  url: string;
  enabled: boolean;
  downloadDirOverride: string | null;
  rule: SubscriptionRule;
}): Promise<Subscription> {
  return request<Subscription>("/api/subscriptions", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function updateSubscription(
  id: number,
  payload: Partial<{
    name: string;
    url: string;
    enabled: boolean;
    downloadDirOverride: string | null;
    rule: SubscriptionRule;
  }>
): Promise<Subscription> {
  return request<Subscription>(`/api/subscriptions/${id}`, {
    method: "PUT",
    body: JSON.stringify(payload)
  });
}

export async function deleteSubscription(id: number): Promise<{ deleted: boolean }> {
  return request<{ deleted: boolean }>(`/api/subscriptions/${id}`, {
    method: "DELETE"
  });
}

export async function refreshSubscription(id: number): Promise<{
  subscription: Subscription;
  createdCount: number;
  queuedCount: number;
}> {
  return request(`/api/subscriptions/${id}/refresh`, {
    method: "POST"
  });
}

export async function listFeedItems(
  options: { subscriptionId?: number; limit?: number } = {}
): Promise<FeedItem[]> {
  const query = new URLSearchParams();
  if (typeof options.subscriptionId === "number") {
    query.set("subscriptionId", String(options.subscriptionId));
  }
  if (typeof options.limit === "number") {
    query.set("limit", String(options.limit));
  }
  const suffix = query.size ? `?${query.toString()}` : "";
  return request<FeedItem[]>(`/api/feed-items${suffix}`);
}

export async function listHomepageAnime(): Promise<HomepageAnime[]> {
  return request<HomepageAnime[]>("/api/discovery/home", undefined, HOMEPAGE_REQUEST_TIMEOUT_MS);
}

export async function searchAnime(keyword: string): Promise<AnimeSearchResult> {
  return request<AnimeSearchResult>(
    `/api/discovery/search?keyword=${encodeURIComponent(keyword)}`,
    undefined,
    20000
  );
}

export async function getAnimeDetail(animeId: string): Promise<AnimeDetail> {
  return request<AnimeDetail>(`/api/discovery/anime/${encodeURIComponent(animeId)}`, undefined, 20000);
}

export interface MetadataPlatformInfo {
  id: string;
  displayName: string;
  enabled: boolean;
  configured: boolean;
  requiresApiKey: boolean;
}

export interface MetadataPlatformTestResult {
  id: string;
  displayName: string;
  ok: boolean;
  count: number;
  reason: string;
  elapsedMs: number;
}

export async function listMetadataPlatforms(): Promise<MetadataPlatformInfo[]> {
  return request<MetadataPlatformInfo[]>("/api/discovery/metadata-platforms");
}

export async function testMetadataPlatform(
  platformId: string,
  keyword: string
): Promise<MetadataPlatformTestResult> {
  return request<MetadataPlatformTestResult>(
    `/api/discovery/metadata-platforms/${encodeURIComponent(platformId)}/test`,
    { method: "POST", body: JSON.stringify({ keyword }) },
    20000
  );
}

export async function listFavoriteAnime(): Promise<FavoriteAnime[]> {
  return request<FavoriteAnime[]>("/api/favorites");
}

export async function createFavoriteAnime(payload: {
  anime: HomepageAnime;
  mikanKeyword?: string;
}): Promise<FavoriteAnime> {
  return request<FavoriteAnime>("/api/favorites", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function deleteFavoriteAnime(animeId: string): Promise<{ deleted: boolean }> {
  return request<{ deleted: boolean }>(`/api/favorites/${encodeURIComponent(animeId)}`, {
    method: "DELETE"
  });
}

/** 一次给一部番挂上多个来源的订阅；省略 providerIds 时用设置里启用的源。 */
export async function createFavoriteSubscription(payload: {
  anime: HomepageAnime;
  mikanKeyword?: string;
  providerIds?: string[];
}): Promise<Subscription[]> {
  return request<Subscription[]>("/api/favorites/subscribe", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function listResourceProviders(): Promise<ResourceProviderSummary[]> {
  return request<ResourceProviderSummary[]>("/api/discovery/providers");
}

export async function testResourceProvider(
  providerId: string,
  keyword: string
): Promise<ResourceProviderFetchStatus & { elapsedMs: number }> {
  return request<ResourceProviderFetchStatus & { elapsedMs: number }>(
    `/api/discovery/providers/${encodeURIComponent(providerId)}/test`,
    { method: "POST", body: JSON.stringify({ keyword }) },
    20000
  );
}

export async function searchMikanDownloads(keyword: string): Promise<MikanDownloadItem[]> {
  return request<MikanDownloadItem[]>(
    `/api/discovery/mikan/search?keyword=${encodeURIComponent(keyword)}`,
    undefined,
    20000
  );
}

export async function createDownload(payload: {
  feedItemId?: number;
  url?: string;
  title?: string;
  imageUrl?: string | null;
  animeId?: string;
  pageUrl?: string;
  sourceType?: "direct" | "magnet" | "torrent-url";
}): Promise<DownloadJob> {
  return request<DownloadJob>("/api/downloads", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function listDownloads(): Promise<DownloadJob[]> {
  return request<DownloadJob[]>("/api/downloads");
}

export async function clearFinishedDownloads(): Promise<{ removedCount: number; gids: string[] }> {
  return request<{ removedCount: number; gids: string[] }>(
    "/api/downloads/clear-finished",
    {
      method: "POST"
    },
    30000
  );
}

export async function pauseDownload(gid: string): Promise<DownloadJob | null> {
  return request<DownloadJob | null>(`/api/downloads/${encodeURIComponent(gid)}/pause`, {
    method: "POST"
  });
}

export async function resumeDownload(gid: string): Promise<DownloadJob | null> {
  return request<DownloadJob | null>(`/api/downloads/${encodeURIComponent(gid)}/resume`, {
    method: "POST"
  });
}

export async function resolveDownloadConflict(
  gid: string,
  strategy: DownloadConflictStrategy
): Promise<DownloadJob> {
  return request<DownloadJob>(`/api/downloads/${encodeURIComponent(gid)}/resolve-conflict`, {
    method: "POST",
    body: JSON.stringify({ strategy })
  });
}

export async function removeDownload(
  gid: string,
  payload: { deleteLocalFiles?: boolean } = {}
): Promise<{ deleted: boolean; deletedLocalFileCount: number }> {
  return request<{ deleted: boolean; deletedLocalFileCount: number }>(`/api/downloads/${encodeURIComponent(gid)}`, {
    method: "DELETE",
    body: JSON.stringify(payload)
  });
}

export async function getDownloadPlaybackInfo(gid: string): Promise<DownloadPlaybackInfo> {
  return request<DownloadPlaybackInfo>(`/api/downloads/${encodeURIComponent(gid)}/playback`);
}

export async function openDownloadPlayer(
  gid: string,
  playerId: "system" | "vlc" | "mpv" | "potplayer" | "mpc-hc" = "system"
): Promise<LaunchPlayerResult> {
  return request<LaunchPlayerResult>(`/api/downloads/${encodeURIComponent(gid)}/open-player`, {
    method: "POST",
    body: JSON.stringify({ playerId })
  });
}

export function getDownloadStreamUrl(gid: string): string {
  const token = readApiToken();
  const query = token ? `?token=${encodeURIComponent(token)}` : "";
  return `${API_BASE_URL}/api/downloads/${encodeURIComponent(gid)}/stream${query}`;
}

export function getDownloadHlsPlaylistUrl(gid: string): string {
  const token = readApiToken();
  const query = token ? `?token=${encodeURIComponent(token)}` : "";
  return `${API_BASE_URL}/api/downloads/${encodeURIComponent(gid)}/hls/master.m3u8${query}`;
}

export function getDownloadSubtitleUrl(gid: string, trackId: string): string {
  const token = readApiToken();
  const query = token ? `?token=${encodeURIComponent(token)}` : "";
  return `${API_BASE_URL}/api/downloads/${encodeURIComponent(gid)}/subtitles/${encodeURIComponent(trackId)}.vtt${query}`;
}

/** ASS/SSA 轨直出原始字节，交给 ass 插件渲染；转 WebVTT 会丢掉全部特效与定位 */
export function getDownloadAssSubtitleUrl(gid: string, trackId: string): string {
  const token = readApiToken();
  const query = token ? `?token=${encodeURIComponent(token)}` : "";
  return `${API_BASE_URL}/api/downloads/${encodeURIComponent(gid)}/subtitles/${encodeURIComponent(trackId)}.ass${query}`;
}

export interface DownloadPlaybackProgress {
  gid: string;
  deliveryMode: DownloadPlaybackInfo["deliveryMode"];
  bufferedSeconds: number | null;
  durationSeconds: number | null;
  finished: boolean;
  error: string | null;
}

export async function getUpdateVersion(): Promise<UpdateVersionInfo> {
  return request<UpdateVersionInfo>("/api/system/version");
}

export async function getUpdateStatus(): Promise<UpdateStatus> {
  return request<UpdateStatus>("/api/system/update");
}

/** 检查要打 GitHub API，网络慢时比普通请求久，单独放宽超时。 */
export async function checkForUpdate(): Promise<UpdateStatus> {
  return request<UpdateStatus>("/api/system/update/check", { method: "POST" }, 30000);
}

/** 下载整包可能要几分钟：这里只等接口返回，进度靠轮询状态。 */
export async function downloadUpdate(version?: string): Promise<UpdateStatus> {
  return request<UpdateStatus>(
    "/api/system/update/download",
    { method: "POST", body: JSON.stringify(version ? { version } : {}) },
    30 * 60 * 1000
  );
}

export async function installUpdate(version?: string): Promise<UpdateStatus> {
  return request<UpdateStatus>(
    "/api/system/update/install",
    { method: "POST", body: JSON.stringify(version ? { version } : {}) },
    30 * 60 * 1000
  );
}

export async function rollbackUpdate(version: string): Promise<UpdateStatus> {
  return request<UpdateStatus>(
    "/api/system/update/rollback",
    { method: "POST", body: JSON.stringify({ version }) },
    30 * 60 * 1000
  );
}

export async function getUpdatePolicy(): Promise<UpdatePolicy> {
  return request<UpdatePolicy>("/api/system/update/policy");
}

export async function saveUpdatePolicy(patch: Partial<UpdatePolicy>): Promise<UpdatePolicy> {
  return request<UpdatePolicy>("/api/system/update/policy", {
    method: "PUT",
    body: JSON.stringify(patch)
  });
}

export async function getUpdateChangelog(limit = 5): Promise<UpdateReleaseSummary[]> {
  return request<UpdateReleaseSummary[]>(`/api/system/update/changelog?limit=${limit}`);
}

/** 源码部署专用：把服务装成 launchd / systemd 单元。 */
export async function installBackgroundService(): Promise<{ manager: string; path: string }> {
  return request<{ manager: string; path: string }>("/api/system/update/service", { method: "POST" }, 30000);
}

export async function clearPlaybackCache(): Promise<{ removedCount: number; freedBytes: number }> {
  return request<{ removedCount: number; freedBytes: number }>("/api/downloads/playback-cache/clear", {
    method: "POST"
  });
}

export async function getDownloadPlaybackProgress(gid: string): Promise<DownloadPlaybackProgress> {
  return request<DownloadPlaybackProgress>(`/api/downloads/${encodeURIComponent(gid)}/playback/progress`);
}

export function resolveDownloadArtworkUrl(download: DownloadJob): string | null {
  const sourceUrl = download.thumbnailUrl ?? download.feedItemImageUrl ?? null;
  if (!sourceUrl) {
    return null;
  }

  try {
    const url = new URL(sourceUrl, window.location.origin);
    if (sourceUrl.startsWith("/api/")) {
      const token = readApiToken();
      if (token) {
        url.searchParams.set("token", token);
      }
    }
    if (API_BASE_URL && sourceUrl.startsWith("/api/")) {
      return `${API_BASE_URL}${url.pathname}${url.search}`;
    }
    return url.toString();
  } catch {
    return sourceUrl;
  }
}

export async function listRecentLogs(limit = 50): Promise<LogEntry[]> {
  return request<LogEntry[]>(`/api/logs/recent?limit=${limit}`);
}

export async function getLogFileInfo(): Promise<{ path: string }> {
  return request<{ path: string }>("/api/logs/file-info");
}

export async function getTunnelStatus(): Promise<TunnelStatusSnapshot> {
  return request<TunnelStatusSnapshot>("/api/tunnel/status");
}

export async function getTunnelPreflight(): Promise<TunnelPreflightIssue[]> {
  return request<TunnelPreflightIssue[]>("/api/tunnel/preflight");
}

export async function listTunnelLogs(limit = 100): Promise<TunnelLogEntry[]> {
  return request<TunnelLogEntry[]>(`/api/tunnel/logs?limit=${limit}`);
}

export async function listDiscoveredTunnels(): Promise<DiscoveredTunnel[]> {
  return request<DiscoveredTunnel[]>("/api/tunnel/discovered");
}

/** 隧道要连 Cloudflare 边缘，比普通请求慢，单独放宽超时。 */
const TUNNEL_ACTION_TIMEOUT_MS = 30000;

export async function startTunnel(): Promise<TunnelStatusSnapshot> {
  return request<TunnelStatusSnapshot>("/api/tunnel/start", { method: "POST" }, TUNNEL_ACTION_TIMEOUT_MS);
}

export async function stopTunnel(): Promise<TunnelStatusSnapshot> {
  return request<TunnelStatusSnapshot>("/api/tunnel/stop", { method: "POST" }, TUNNEL_ACTION_TIMEOUT_MS);
}

export async function restartTunnel(): Promise<TunnelStatusSnapshot> {
  return request<TunnelStatusSnapshot>("/api/tunnel/restart", { method: "POST" }, TUNNEL_ACTION_TIMEOUT_MS);
}

export async function detectAgents(): Promise<AgentDetectionResult[]> {
  return request<AgentDetectionResult[]>("/api/agents/detect");
}

export async function installAgent(host: AgentHost): Promise<AgentInstallResult> {
  return request<AgentInstallResult>("/api/agents/install", {
    method: "POST",
    body: JSON.stringify({ host })
  });
}

export async function uninstallAgent(host: AgentHost): Promise<AgentInstallResult> {
  return request<AgentInstallResult>("/api/agents/uninstall", {
    method: "POST",
    body: JSON.stringify({ host })
  });
}

export async function getLocalAiStatus(): Promise<LocalAiStatus> {
  return request<LocalAiStatus>("/api/local-ai/status");
}

export async function sendLocalAiChat(messages: LocalAiChatMessage[], signal?: AbortSignal): Promise<LocalAiChatResponse> {
  return request<LocalAiChatResponse>("/api/local-ai/chat", {
    method: "POST",
    body: JSON.stringify({ messages }),
    signal
  }, 120000);
}

export async function doctorAgent(host: AgentHost): Promise<AgentDoctorResult> {
  return request<AgentDoctorResult>(`/api/agents/doctor?host=${encodeURIComponent(host)}`);
}

/** 备份包可能不小，恢复还要落盘重开库，比普通请求慢得多。 */
const BACKUP_ACTION_TIMEOUT_MS = 120000;

export async function listBackups(): Promise<BackupListResult> {
  return request<BackupListResult>("/api/system/backups", undefined, BACKUP_ACTION_TIMEOUT_MS);
}

export async function createBackup(reason = "manual"): Promise<CreateBackupResult> {
  return request<CreateBackupResult>(
    "/api/system/backups",
    { method: "POST", body: JSON.stringify({ reason }) },
    BACKUP_ACTION_TIMEOUT_MS
  );
}

export async function restoreBackup(name: string): Promise<RestoreBackupResult> {
  return request<RestoreBackupResult>(
    `/api/system/backups/${encodeURIComponent(name)}/restore`,
    { method: "POST" },
    BACKUP_ACTION_TIMEOUT_MS
  );
}

export async function deleteBackup(name: string): Promise<{ deleted: boolean; backups: BackupListResult["backups"] }> {
  return request<{ deleted: boolean; backups: BackupListResult["backups"] }>(
    `/api/system/backups/${encodeURIComponent(name)}`,
    { method: "DELETE" },
    BACKUP_ACTION_TIMEOUT_MS
  );
}

/**
 * 下载要带 Authorization 头，普通 <a download> 做不到，
 * 只能自己 fetch 成 blob 再触发一次本地下载。
 */
export async function downloadBackup(name: string): Promise<void> {
  const token = readApiToken();
  const response = await fetch(`${API_BASE_URL}/api/system/backups/${encodeURIComponent(name)}/download`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined
  });
  if (!response.ok) {
    throw new ApiRequestError(t("api.invalidResponse", { status: response.status }), "INVALID_RESPONSE");
  }
  const blob = await response.blob();
  const objectUrl = URL.createObjectURL(blob);
  try {
    const anchor = document.createElement("a");
    anchor.href = objectUrl;
    anchor.download = name;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
  } finally {
    // 立刻 revoke 会让部分浏览器来不及取数据，挪到下一轮事件循环。
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 0);
  }
}

export async function importBackup(file: File): Promise<RestoreBackupResult> {
  const token = readApiToken();
  const response = await fetch(`${API_BASE_URL}/api/system/backups/import`, {
    method: "POST",
    headers: {
      "Content-Type": "application/zip",
      ...(token ? { Authorization: `Bearer ${token}` } : {})
    },
    body: file
  });
  const payload = (await response.json()) as ApiResponse<RestoreBackupResult>;
  if (!payload.ok) {
    throw new ApiRequestError(payload.error.message, payload.error.code);
  }
  return payload.data;
}

export async function openDataDir(): Promise<OpenDataDirResult> {
  return request<OpenDataDirResult>("/api/system/open-data-dir", { method: "POST" });
}

export const getCloudStatus = () => request<{ connected: boolean; configured: boolean }>("/api/cloud/status");
export const connectCloud = () => request<{ url: string }>("/api/cloud/connect", { method: "POST" });
export const disconnectCloud = () => request<{ connected: boolean }>("/api/cloud/disconnect", { method: "POST" });

export interface DriveNetworkStatus {
  enabled: boolean;
  selectedIP: string;
  scannedAt?: string;
  partial?: boolean;
  candidateCount?: number;
  results: Array<{ ip: string; available: boolean; latencyMs: number; verifiedAt: number; warning: string; error: string }>;
}
export const getDriveNetwork = () => request<DriveNetworkStatus>("/api/cloud/network");
export const setDriveNetwork = (enabled: boolean) => request<DriveNetworkStatus>("/api/cloud/network", { method: "PATCH", body: JSON.stringify({ enabled }) });
export const scanDriveNetwork = (candidates: string) => request<DriveNetworkStatus>("/api/cloud/network/scan", { method: "POST", body: JSON.stringify({ candidates }) }, 70000);
