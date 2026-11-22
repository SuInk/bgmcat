import type {
  AgentDetectionResult,
  AgentDoctorResult,
  AgentHost,
  AgentInstallResult,
  AnimeCatalogQuery,
  AnimeCatalogResult,
  AnimeDetail,
  AnimeSearchResult,
  AnimeStreamingLink,
  AnimeSubscriptionOptions,
  ApiResponse,
  AppSettings,
  BackupListResult,
  CloudProviderType,
  CreateBackupResult,
  DownloadClientType,
  DownloadJob,
  DownloadConflictStrategy,
  DownloadPlaybackInfo,
  FeedItem,
  FavoriteAnime,
  HiddenAnime,
  HomepageAnime,
  LaunchPlayerResult,
  LocalAiChatMessage,
  LocalAiChatResponse,
  LocalAiStatus,
  MangaDetailResult,
  MangaDiscoverResult,
  MangaPagesResult,
  MangaReadProgress,
  AuthLoginResult,
  AuthSessionSummary,
  AuthStatusResponse,
  DianaPairCodeResult,
  DianaPairStatusResult,
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

import { describeServerFailure } from "./lib/gatewayError";
import { readStorageItem } from "./lib/storageKeys";

const API_BASE_URL = resolveApiBaseUrl();
const STORAGE_KEY = "bgmcat.api-token";
const LEGACY_STORAGE_KEY = "anime-rss-api-token";
const REQUEST_TIMEOUT_MS = 8000;
const HOMEPAGE_REQUEST_TIMEOUT_MS = 30000;
const PLAYBACK_REQUEST_TIMEOUT_MS = 45000;

export class ApiRequestError extends Error {
  constructor(
    message: string,
    readonly code?: string,
    readonly status = 0
  ) {
    super(message);
    this.name = "ApiRequestError";
  }

  get unreachable(): boolean {
    return (
      this.code === "NETWORK_ERROR" ||
      this.code === "REQUEST_TIMEOUT" ||
      this.code === "GATEWAY_ERROR"
    );
  }
}

export function isBackendUnreachable(error: unknown): boolean {
  return error instanceof ApiRequestError && error.unreachable;
}

interface DesktopShellBridge {
  apiToken?: string;
  platform?: string;
}

/** 桌面版原生「选择文件夹」对话框；网页里返回 null（没有这个能力），取消也返回 null。 */
export async function pickDesktopDirectory(): Promise<string | null> {
  const bridge = (window as unknown as { animeRssShell?: { pickDirectory?: () => Promise<string | null> } }).animeRssShell;
  return bridge?.pickDirectory ? bridge.pickDirectory() : null;
}

export function canPickDesktopDirectory(): boolean {
  const bridge = (window as unknown as { animeRssShell?: { pickDirectory?: unknown } }).animeRssShell;
  return typeof bridge?.pickDirectory === "function";
}

/** The desktop shell's OS, e.g. "darwin"; empty in a normal browser. */
export function readDesktopShellPlatform(): string {
  const bridge = (window as unknown as { animeRssShell?: DesktopShellBridge }).animeRssShell;
  return typeof bridge?.platform === "string" ? bridge.platform : "";
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
  return Boolean(readDesktopShellToken()) || /BgmcatShell|AnimeRSSShell/i.test(window.navigator.userAgent);
}

interface AndroidShellBridge {
  isAndroidApp?: () => boolean;
  getAppVersion?: () => string;
  getActiveServerLabel?: () => string;
  openServerSwitcher?: () => void;
}

function readAndroidBridge(): AndroidShellBridge | undefined {
  return (window as unknown as { bgmcatAndroid?: AndroidShellBridge }).bgmcatAndroid;
}

export function isAndroidAppRuntime(): boolean {
  try {
    return Boolean(readAndroidBridge()?.isAndroidApp?.()) || /BgmcatAndroid/i.test(window.navigator.userAgent);
  } catch {
    return false;
  }
}

export function readAndroidServerLabel(): string {
  try {
    return readAndroidBridge()?.getActiveServerLabel?.() || window.location.host;
  } catch {
    return window.location.host;
  }
}

export function openAndroidServerSwitcher(): void {
  try {
    readAndroidBridge()?.openServerSwitcher?.();
  } catch {
    // ignore
  }
}

export function readApiToken(): string {
  // A token stored by the user (or by a rotation) wins so the session survives a rotate;
  // the shell-injected one is the fallback that keeps the desktop app token-free.
  return readStorageItem(STORAGE_KEY, LEGACY_STORAGE_KEY) || readDesktopShellToken();
}

export function persistApiToken(token: string): void {
  window.localStorage.setItem(STORAGE_KEY, token);
}

export function clearApiToken(): void {
  window.localStorage.removeItem(STORAGE_KEY);
  window.localStorage.removeItem(LEGACY_STORAGE_KEY);
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
      credentials: "same-origin",
      ...init,
      headers,
      signal: controller.signal
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw new ApiRequestError(
        timedOut ? t("api.requestTimeout") : "请求已取消。",
        timedOut ? "REQUEST_TIMEOUT" : "REQUEST_ABORTED",
        0
      );
    }
    throw new ApiRequestError(
      "连不上 Bangumi Cat 后端服务",
      "NETWORK_ERROR",
      0
    );
  } finally {
    window.clearTimeout(timeout);
    init?.signal?.removeEventListener("abort", abortFromCaller);
  }

  const responseText = await response.text();
  let payload: ApiResponse<T>;
  try {
    payload = JSON.parse(responseText) as ApiResponse<T>;
  } catch {
    if (response.status >= 500) {
      throw new ApiRequestError(
        describeServerFailure(response.status, responseText),
        "GATEWAY_ERROR",
        response.status
      );
    }
    throw new ApiRequestError(
      t("api.invalidResponse", { status: response.status }),
      "INVALID_RESPONSE",
      response.status
    );
  }

  if (!payload.ok) {
    if (
      response.status === 401 &&
      payload.error.code === "AUTH_INVALID" &&
      !path.startsWith("/api/auth/") &&
      !isDesktopShellRuntime()
    ) {
      window.dispatchEvent(new CustomEvent("bgmcat:unauthorized"));
    }
    throw new ApiRequestError(
      normalizeApiErrorMessage(payload.error.message, payload.error.code),
      payload.error.code,
      response.status
    );
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

export async function getAuthStatus(): Promise<AuthStatusResponse> {
  return request<AuthStatusResponse>("/api/auth/status");
}

export async function passwordLogin(username: string, password: string): Promise<AuthLoginResult> {
  return request<AuthLoginResult>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password })
  });
}

export async function changeAdminCredentials(payload: {
  newUsername: string;
  newPassword: string;
}): Promise<{ updated: boolean; username: string }> {
  return request<{ updated: boolean; username: string }>("/api/auth/password", {
    method: "POST",
    body: JSON.stringify(payload)
  });
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

/** Google 登录入口：整页跳到服务端，由服务端 302 到 Google。 */
export function googleLoginUrl(): string {
  return `${API_BASE_URL}/api/auth/google/start`;
}

/** 通用 OIDC / SSO 登录入口：整页跳到服务端，由服务端 302 到 IdP。 */
export function oidcLoginUrl(): string {
  return `${API_BASE_URL}/api/auth/oidc/start`;
}

export async function exchangeOAuthLogin(code: string): Promise<GithubLoginResult> {
  return request<GithubLoginResult>("/api/auth/oauth/exchange", {
    method: "POST",
    body: JSON.stringify({ code })
  });
}

export async function exchangeGithubLogin(code: string): Promise<GithubLoginResult> {
  return exchangeOAuthLogin(code);
}

export async function dianaPasswordLogin(username: string, password: string): Promise<AuthLoginResult> {
  return request<AuthLoginResult>("/api/auth/diana/login", {
    method: "POST",
    body: JSON.stringify({ username, password })
  });
}

export async function createDianaPairing(): Promise<DianaPairCodeResult> {
  return request<DianaPairCodeResult>("/api/auth/diana/pair", {
    method: "POST",
    body: JSON.stringify({})
  });
}

export async function pollDianaPairing(pollToken: string): Promise<DianaPairStatusResult> {
  return request<DianaPairStatusResult>("/api/auth/diana/pair/status", {
    method: "POST",
    body: JSON.stringify({ pollToken })
  });
}

export async function claimDianaPairing(code: string): Promise<AuthLoginResult> {
  return request<AuthLoginResult>("/api/auth/diana/pair/claim", {
    method: "POST",
    body: JSON.stringify({ code })
  });
}

export async function createQrLoginTicket(): Promise<{ code: string; username: string; expiresInSeconds: number }> {
  return request<{ code: string; username: string; expiresInSeconds: number }>("/api/auth/qr-ticket", {
    method: "POST",
    body: JSON.stringify({})
  });
}

/** 获取服务端生成的二维码 SVG（携带当前认证信息）。 */
export async function fetchRemoteQrSvg(text: string): Promise<string> {
  const token = readApiToken();
  const headers = new Headers();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  const response = await fetch(`${API_BASE_URL}/api/remote/qrcode?text=${encodeURIComponent(text)}`, {
    credentials: "same-origin",
    headers
  });
  const body = await response.text();
  if (!response.ok) {
    let message = `生成二维码失败（HTTP ${response.status}）`;
    try {
      const parsed = JSON.parse(body) as { error?: string };
      if (parsed.error) {
        message = parsed.error;
      }
    } catch {
      // ignore non-JSON error
    }
    throw new ApiRequestError(message, "QR_ERROR", response.status);
  }
  return body;
}

export async function listAuthSessions(): Promise<AuthSessionSummary[]> {
  return request<AuthSessionSummary[]>("/api/auth/sessions");
}

export async function revokeAuthSession(id: number): Promise<{ revoked: boolean; current?: boolean }> {
  return request<{ revoked: boolean; current?: boolean }>(`/api/auth/sessions/${id}`, { method: "DELETE" });
}

export async function revokeOtherAuthSessions(): Promise<{ revoked: number }> {
  return request<{ revoked: number }>("/api/auth/sessions/revoke-others", { method: "POST" });
}

export async function logoutSession(): Promise<{ revoked: boolean }> {
  return request<{ revoked: boolean }>("/api/auth/logout", { method: "POST" });
}

export interface BootstrapResponse {
  auth: AuthStatusResponse;
  status?: SystemStatus;
  settings?: AppSettings;
  favorites?: FavoriteAnime[];
  hidden?: HiddenAnime[];
  subscriptions?: Subscription[];
  downloads?: DownloadJob[];
  homepage?: HomepageAnime[] | null;
  homepageFresh?: boolean;
}

export async function getBootstrapPayload(): Promise<BootstrapResponse> {
  return request<BootstrapResponse>("/api/bootstrap", undefined, 15000);
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

export async function listCatalogAnime(
  query: AnimeCatalogQuery = {},
  signal?: AbortSignal
): Promise<AnimeCatalogResult> {
  const params = new URLSearchParams();
  if (query.keyword?.trim()) params.set("keyword", query.keyword.trim());
  if (query.format && query.format !== "all") params.set("format", query.format);
  if (query.region && query.region !== "all") params.set("region", query.region);
  if (query.status && query.status !== "all") params.set("status", query.status);
  if (query.year && query.year !== "all") params.set("year", query.year);
  if (query.season && query.season !== "all") params.set("season", query.season);
  if (query.genre && query.genre !== "all") params.set("genre", query.genre);
  if (typeof query.minRating === "number" && query.minRating > 0) params.set("minRating", String(query.minRating));
  if (query.sort) params.set("sort", query.sort);
  if (typeof query.limit === "number") params.set("limit", String(query.limit));
  if (typeof query.offset === "number" && query.offset > 0) params.set("offset", String(query.offset));
  const suffix = params.size ? `?${params.toString()}` : "";
  return request<AnimeCatalogResult>(`/api/discovery/catalog${suffix}`, signal ? { signal } : undefined, 25000);
}

export async function searchAnime(keyword: string): Promise<AnimeSearchResult> {
  return request<AnimeSearchResult>(
    `/api/discovery/search?keyword=${encodeURIComponent(keyword)}`,
    undefined,
    20000
  );
}

export async function getAnimeDetail(
  animeId: string,
  options?: { progressive?: boolean; keyword?: string; signal?: AbortSignal }
): Promise<AnimeDetail> {
  const params = new URLSearchParams();
  if (options?.progressive) {
    params.set("progressive", "1");
  }
  if (options?.keyword?.trim()) {
    params.set("keyword", options.keyword.trim());
  }
  const suffix = params.size ? `?${params.toString()}` : "";
  return request<AnimeDetail>(
    `/api/discovery/anime/${encodeURIComponent(animeId)}${suffix}`,
    options?.signal ? { signal: options.signal } : undefined,
    20000
  );
}

// Keep hover and click on the same request. Limit retained summaries and expire them.
const animeSummaries = new Map<string, { expires: number; value: Promise<AnimeDetail> }>();
export function getAnimeSummary(animeId: string): Promise<AnimeDetail> {
 const cached = animeSummaries.get(animeId);
 if (cached && cached.expires > Date.now()) return cached.value;
 const value = request<AnimeDetail>(`/api/discovery/anime/${encodeURIComponent(animeId)}?view=summary`, undefined, 10000);
 const entry = { expires: Date.now() + 5 * 60 * 1000, value };
 animeSummaries.delete(animeId);
 animeSummaries.set(animeId, entry);
 while (animeSummaries.size > 64) animeSummaries.delete(animeSummaries.keys().next().value!);
 void value.catch(() => { if (animeSummaries.get(animeId) === entry) animeSummaries.delete(animeId); });
 return value;
}

const animeStreamingCache = new Map<string, { expires: number; value: Promise<AnimeStreamingLink[]> }>();
export function getAnimeStreamingLinks(animeId: string): Promise<AnimeStreamingLink[]> {
  const key = animeId.trim();
  const cached = animeStreamingCache.get(key);
  if (cached && cached.expires > Date.now()) {
    return cached.value;
  }
  const value = request<{ streamingLinks: AnimeStreamingLink[] }>(
    `/api/discovery/streaming/${encodeURIComponent(key)}`,
    undefined,
    15000
  ).then((res) => res.streamingLinks ?? []);
  const entry = { expires: Date.now() + 10 * 60 * 1000, value };
  animeStreamingCache.delete(key);
  animeStreamingCache.set(key, entry);
  while (animeStreamingCache.size > 128) {
    animeStreamingCache.delete(animeStreamingCache.keys().next().value!);
  }
  void value.catch(() => {
    if (animeStreamingCache.get(key) === entry) {
      animeStreamingCache.delete(key);
    }
  });
  return value;
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

export async function listHiddenAnime(): Promise<HiddenAnime[]> {
  return request<HiddenAnime[]>("/api/discovery/hidden");
}

export async function hideHomepageAnime(anime: HomepageAnime): Promise<HiddenAnime> {
  return request<HiddenAnime>("/api/discovery/hidden", {
    method: "POST",
    body: JSON.stringify({ anime })
  });
}

export async function unhideHomepageAnime(animeId: string): Promise<{ deleted: boolean }> {
  return request<{ deleted: boolean }>(`/api/discovery/hidden/${encodeURIComponent(animeId)}`, {
    method: "DELETE"
  });
}

export async function clearHiddenAnime(): Promise<{ deletedCount: number }> {
  return request<{ deletedCount: number }>("/api/discovery/hidden", {
    method: "DELETE"
  });
}

/** 一次给一部番挂上多个来源的订阅；省略 providerIds 时用设置里启用的源。 */
export async function createFavoriteSubscription(payload: {
  anime: HomepageAnime;
  mikanKeyword?: string;
  providerIds?: string[];
  options?: AnimeSubscriptionOptions;
}): Promise<Subscription[]> {
  return request<Subscription[]>("/api/favorites/subscribe", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function unsubscribeFavoriteAnime(animeId: string): Promise<{ deletedCount: number }> {
  return request<{ deletedCount: number }>(`/api/favorites/${encodeURIComponent(animeId)}/subscriptions`, {
    method: "DELETE"
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

export interface CreateDownloadPayload {
  feedItemId?: number;
  url?: string;
  /** .torrent 拉取超时或解析失败时自动降级到的磁力链接 */
  fallbackUrl?: string;
  title?: string;
  animeTitle?: string;
  imageUrl?: string | null;
  animeId?: string;
  pageUrl?: string;
  sourceType?: "direct" | "magnet" | "torrent-url";
  /** 不传则按设置里的 defaultSaveTo */
  saveTo?: "local" | "cloud";
  /** 本机保存目录（绝对路径）；不传用设置里的下载目录 */
  directory?: string;
}

export async function createDownload(payload: CreateDownloadPayload): Promise<DownloadJob> {
  return request<DownloadJob>("/api/downloads", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export interface CreateDownloadsBatchResult {
  queuedCount: number;
  skippedCount: number;
  failedCount: number;
  errors?: string[];
  items: DownloadJob[];
}

export async function createDownloadsBatch(payload: {
  items: CreateDownloadPayload[];
  saveTo?: "local" | "cloud";
  directory?: string;
  animeTitle?: string;
}): Promise<CreateDownloadsBatchResult> {
  return request<CreateDownloadsBatchResult>(
    "/api/downloads/batch",
    {
      method: "POST",
      body: JSON.stringify(payload)
    },
    25000
  );
}

export async function archiveDownload(gid: string): Promise<DownloadJob> {
  return request<DownloadJob>(`/api/downloads/${encodeURIComponent(gid)}/archive`, {
    method: "POST"
  });
}

export async function cancelDownloadArchive(gid: string): Promise<DownloadJob> {
  return request<DownloadJob>(`/api/downloads/${encodeURIComponent(gid)}/archive`, {
    method: "DELETE"
  });
}

export function getDownloadEventsUrl(): string {
  const token = readApiToken();
  const query = token ? `?token=${encodeURIComponent(token)}` : "";
  return `${API_BASE_URL}/api/downloads/events${query}`;
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
  return request<DownloadPlaybackInfo>(
    `/api/downloads/${encodeURIComponent(gid)}/playback`,
    undefined,
    PLAYBACK_REQUEST_TIMEOUT_MS
  );
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
    credentials: "same-origin",
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

export interface SystemStorageDirectory {
  key: "downloads" | "database" | "cache" | "state" | "backups";
  label: string;
  path: string;
  bytes: number;
  files: number;
}

export interface SystemStorageUsage {
  dataDir: string;
  downloadDir: string;
  diskTotalBytes: number;
  diskFreeBytes: number;
  appBytes: number;
  appFiles: number;
  cacheBytes: number;
  directories: SystemStorageDirectory[];
}

export async function getSystemStorage(): Promise<SystemStorageUsage> {
  return request<SystemStorageUsage>("/api/system/storage");
}

export async function openDataDir(): Promise<OpenDataDirResult> {
  return request<OpenDataDirResult>("/api/system/open-data-dir", { method: "POST" });
}

export interface CloudProviderStatus {
  provider: CloudProviderType;
  displayName: string;
  authKind: "oauth" | "basic";
  connected: boolean;
  configured: boolean;
  builtin?: boolean;
  folderId?: string;
}

export const listCloudProviders = () => request<CloudProviderStatus[]>("/api/cloud/providers");

export const getCloudStatus = (provider?: CloudProviderType) =>
  request<{ connected: boolean; configured: boolean; builtin?: boolean; provider?: CloudProviderType; displayName?: string; folderId?: string }>(
    `/api/cloud/status${provider ? `?${new URLSearchParams({ provider })}` : ""}`
  );

export const connectCloud = (options?: {
  provider?: CloudProviderType;
  redirectUri?: string;
  returnTo?: string;
  webdavUrl?: string;
  webdavUsername?: string;
  webdavPassword?: string;
  webdavRootPath?: string;
}) =>
  request<{ url?: string; connected?: boolean; provider?: CloudProviderType; displayName?: string }>("/api/cloud/connect", {
    method: "POST",
    body: JSON.stringify(options ?? {})
  });

export const disconnectCloud = (provider?: CloudProviderType) =>
  request<{ connected: boolean; provider?: CloudProviderType }>("/api/cloud/disconnect", {
    method: "POST",
    body: JSON.stringify(provider ? { provider } : {})
  });

export interface DriveNetworkResultRow {
  ip: string;
  available: boolean;
  tcpLatencyMs?: number;
  latencyMs: number;
  verifiedAt: number;
  warning: string;
  error: string;
  sni?: string;
}

export interface DriveNetworkStatus {
  enabled: boolean;
  bypassTun?: boolean;
  interface?: string;
  sniMode: "original" | "ech" | "fragment" | "custom" | "none";
  sniHost: string;
  selectedIP: string;
  selectedSNI?: string;
  preferredIP?: string;
  scannedAt?: string;
  partial?: boolean;
  candidateCount?: number;
  tunWarning?: string;
  results: DriveNetworkResultRow[];
}
export const getDriveNetwork = () => request<DriveNetworkStatus>("/api/cloud/network");
export const setDriveNetwork = (settings: Partial<Pick<DriveNetworkStatus, "enabled" | "bypassTun" | "sniMode" | "sniHost" | "preferredIP">>) => request<DriveNetworkStatus>("/api/cloud/network", { method: "PATCH", body: JSON.stringify(settings) });
export const scanDriveNetwork = (candidates: string) => request<DriveNetworkStatus>("/api/cloud/network/scan", { method: "POST", body: JSON.stringify({ candidates }) }, 70000);
export const probeDriveIP = (ip: string) => request<DriveNetworkStatus>("/api/cloud/network/probe-one", { method: "POST", body: JSON.stringify({ ip }) }, 15000);

export interface NetworkProbeTargetSummary {
  id: string;
  name: string;
  group: string;
  hosts: string[];
  custom: boolean;
  enabled: boolean;
  selectedIP: string;
  availableCount: number;
  totalCount: number;
  fastestMs?: number | null;
}

export interface NetworkProbeTargetStatus extends DriveNetworkStatus {
  id: string;
  name: string;
  group: string;
  hosts: string[];
  custom: boolean;
  presetSeeds?: string[];
}

export const listNetworkTargets = () => request<NetworkProbeTargetSummary[]>("/api/network/targets");
export const addNetworkTarget = (host: string, name?: string) =>
  request<NetworkProbeTargetStatus>("/api/network/targets", {
    method: "POST",
    body: JSON.stringify(name ? { host, name } : { host })
  });
export const deleteNetworkTarget = (id: string) =>
  request<{ deleted: boolean; id: string }>(`/api/network/targets/${encodeURIComponent(id)}`, {
    method: "DELETE"
  });
export const getNetworkTarget = (id: string) =>
  request<NetworkProbeTargetStatus>(`/api/network/targets/${encodeURIComponent(id)}`);
export const patchNetworkTarget = (
  id: string,
  settings: Partial<Pick<DriveNetworkStatus, "enabled" | "bypassTun" | "sniMode" | "sniHost" | "preferredIP">>
) =>
  request<NetworkProbeTargetStatus>(`/api/network/targets/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify(settings)
  });
export const scanNetworkTarget = (id: string, candidates: string) =>
  request<NetworkProbeTargetStatus>(
    `/api/network/targets/${encodeURIComponent(id)}/scan`,
    { method: "POST", body: JSON.stringify({ candidates }) },
    70000
  );
export const probeNetworkTargetIP = (id: string, ip: string) =>
  request<NetworkProbeTargetStatus>(
    `/api/network/targets/${encodeURIComponent(id)}/probe-one`,
    { method: "POST", body: JSON.stringify({ ip }) },
    15000
  );

export interface DownloadEngineTestResult {
  client: DownloadClientType;
  displayName: string;
  ok: boolean;
  version: string;
  error: string;
  latencyMs: number;
}

export const testDownloadEngine = (payload: Record<string, unknown>) =>
  request<DownloadEngineTestResult>("/api/downloads/engines/test", {
    method: "POST",
    body: JSON.stringify(payload)
  });

export interface CloudFolder { id: string; name: string }
export const listCloudFolders = (parent: string, pageToken = "", provider?: CloudProviderType) => {
  const qs = new URLSearchParams({ parent, pageToken });
  if (provider) qs.set("provider", provider);
  return request<{ files: CloudFolder[]; nextPageToken?: string }>(`/api/cloud/folders?${qs.toString()}`);
};
export const createCloudFolder = (parent: string, provider?: CloudProviderType, name?: string) =>
  request<CloudFolder>("/api/cloud/folders", {
    method: "POST",
    body: JSON.stringify({ parent, ...(provider ? { provider } : {}), ...(name ? { name } : {}) })
  });
export const checkCloudFolder = (folderId: string, provider?: CloudProviderType) =>
  request<{ verified: boolean }>(
    "/api/cloud/check",
    { method: "POST", body: JSON.stringify({ folderId, ...(provider ? { provider } : {}) }) },
    45000
  );

export type FileEntryKind =
  | "folder"
  | "video"
  | "audio"
  | "image"
  | "pdf"
  | "subtitle"
  | "text"
  | "archive"
  | "torrent"
  | "other";

export interface FileBrowserEntry {
  id: string;
  name: string;
  displayName?: string;
  originalPath?: string;
  path: string;
  isDir: boolean;
  mimeType?: string;
  size: number;
  modTime: string;
  ext: string;
  kind: FileEntryKind;
  playable: boolean;
  mediaId: string;
}

export interface LocalFilesResponse {
  rootPath: string;
  path: string;
  freeBytes: number;
  totalBytes: number;
  trashCount?: number;
  trashBytes?: number;
  entries: FileBrowserEntry[];
}

export interface CloudDriveQuota {
  usageBytes: number;
  limitBytes: number;
  userEmail: string;
}

export interface CloudFilesResponse {
  provider?: CloudProviderType;
  providerName?: string;
  connected: boolean;
  configured: boolean;
  archiveFolderId: string;
  parent: string;
  parentName: string;
  nextPageToken?: string;
  quota?: CloudDriveQuota;
  entries: FileBrowserEntry[];
}

export const listLocalFiles = (path = "") =>
  request<LocalFilesResponse>(`/api/files/local${path ? `?${new URLSearchParams({ path })}` : ""}`);

export const mkdirLocalFile = (parent: string, name: string) =>
  request<{ name: string; path: string }>("/api/files/local/mkdir", {
    method: "POST",
    body: JSON.stringify({ parent, name })
  });

export const deleteLocalFile = (path: string, permanent = false) => {
  const qs = new URLSearchParams({ path });
  if (permanent) qs.set("permanent", "1");
  return request<{ deleted: boolean; path: string; trashed?: boolean; trashPath?: string }>(`/api/files/local?${qs.toString()}`, {
    method: "DELETE"
  });
};

export const restoreLocalTrashFile = (path: string) =>
  request<{ restored: boolean; path: string }>("/api/files/local/trash/restore", {
    method: "POST",
    body: JSON.stringify({ path })
  });

export const emptyLocalTrash = () =>
  request<{ deletedCount: number; deletedBytes: number }>("/api/files/local/trash/empty", {
    method: "POST",
    body: JSON.stringify({})
  });

export function getLocalFileContentUrl(path: string, download = false): string {
  const qs = new URLSearchParams({ path });
  if (download) qs.set("download", "1");
  const token = readApiToken();
  if (token) qs.set("token", token);
  return `${API_BASE_URL}/api/files/local/content?${qs.toString()}`;
}

export function getCloudFileContentUrl(
  id: string,
  download = false,
  provider?: CloudProviderType,
  meta?: { name?: string; mimeType?: string }
): string {
  const qs = new URLSearchParams({ id });
  if (provider) qs.set("provider", provider);
  if (meta?.name) qs.set("name", meta.name);
  if (meta?.mimeType) qs.set("mime", meta.mimeType);
  if (download) qs.set("download", "1");
  const token = readApiToken();
  if (token) qs.set("token", token);
  return `${API_BASE_URL}/api/files/cloud/content?${qs.toString()}`;
}

export async function fetchFileTextPreview(
  url: string,
  maxBytes = 256 * 1024,
  signal?: AbortSignal
): Promise<{ supported: boolean; text: string; clipped: boolean }> {
  const headers = new Headers({
    Range: `bytes=0-${maxBytes - 1}`
  });
  const token = readApiToken();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  const response = await fetch(url, {
    credentials: "same-origin",
    headers,
    signal
  });
  if (!response.ok) {
    let message = `读取失败（HTTP ${response.status}）`;
    try {
      const parsed = (await response.json()) as ApiResponse<unknown>;
      if (!parsed.ok && parsed.error?.message) {
        message = parsed.error.message;
      }
    } catch {
      // ignore non-json body
    }
    throw new ApiRequestError(message, "PREVIEW_FAILED");
  }
  const ct = (response.headers.get("Content-Type") || "").toLowerCase();
  const isTextLike =
    ct.startsWith("text/") ||
    ct.includes("json") ||
    ct.includes("xml") ||
    ct.includes("javascript") ||
    ct.includes("yaml");
  if (!isTextLike) {
    return { supported: false, text: "", clipped: false };
  }
  const buf = await response.arrayBuffer();
  const clipped =
    response.headers.get("X-Preview-Clipped") === "1" ||
    buf.byteLength >= maxBytes;
  const text = new TextDecoder("utf-8", { fatal: false }).decode(buf);
  return { supported: true, text, clipped };
}

export const listCloudFiles = (params: { parent?: string; pageToken?: string; q?: string } = {}) => {
  const qs = new URLSearchParams();
  if (params.parent) qs.set("parent", params.parent);
  if (params.pageToken) qs.set("pageToken", params.pageToken);
  if (params.q) qs.set("q", params.q);
  const query = qs.toString();
  return request<CloudFilesResponse>(`/api/files/cloud${query ? `?${query}` : ""}`, undefined, 25000);
};

export const mkdirCloudFile = (parent: string, name: string) =>
  request<{ id: string; name: string; mimeType?: string }>("/api/files/cloud/mkdir", {
    method: "POST",
    body: JSON.stringify({ parent, name })
  });

export const deleteCloudFile = (id: string) =>
  request<{ deleted: boolean; id: string; trashed?: boolean }>(`/api/files/cloud?${new URLSearchParams({ id })}`, {
    method: "DELETE"
  });

// ---------- 分享管理（管理员）与免登录访客页 ----------

export type ShareTargetInput =
  | { kind: "download"; gid: string; label?: string }
  | { kind: "local"; path: string; label?: string }
  | { kind: "cloud"; id: string; name: string; mimeType?: string; size?: number; isDir?: boolean; label?: string };

export interface ShareItemSummary {
  id: string;
  label: string;
  fileName: string;
  ext: string;
  kind: FileEntryKind;
  size: number;
  playable: boolean;
}

export interface ShareRecord {
  code: string;
  title: string;
  hasPasscode: boolean;
  passcode: string;
  allowDownload: boolean;
  expiresAt: string | null;
  expired: boolean;
  createdAt: string;
  viewCount: number;
  itemCount: number;
  playableCount: number;
  totalSize: number;
  items: ShareItemSummary[];
}

export interface PublicShareView {
  code: string;
  title: string;
  locked: boolean;
  hasPasscode: boolean;
  allowDownload: boolean;
  expiresAt: string | null;
  createdAt: string;
  itemCount: number;
  playableCount: number;
  totalSize: number;
  items: ShareItemSummary[];
  unlockToken?: string;
}

export const listShares = () => request<ShareRecord[]>("/api/shares");

export const createShare = (payload: {
  title?: string;
  expiresInHours?: number;
  passcode?: string;
  allowDownload?: boolean;
  targets: ShareTargetInput[];
}) =>
  request<ShareRecord>(
    "/api/shares",
    {
      method: "POST",
      body: JSON.stringify(payload)
    },
    25000
  );

export const updateShare = (
  code: string,
  payload: {
    title?: string;
    allowDownload?: boolean;
    passcode?: string;
    expiresInHours?: number;
  }
) =>
  request<ShareRecord>(`/api/shares/${encodeURIComponent(code)}`, {
    method: "PATCH",
    body: JSON.stringify(payload)
  });

export const deleteShare = (code: string) =>
  request<{ deleted: boolean; code: string }>(`/api/shares/${encodeURIComponent(code)}`, {
    method: "DELETE"
  });

export const clearExpiredShares = () =>
  request<{ removedCount: number }>("/api/shares?expired=1", {
    method: "DELETE"
  });

async function publicRequest<T>(
  path: string,
  init?: RequestInit,
  unlockToken = "",
  timeoutMs = 15000
): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  const timeout = window.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  const abortFromCaller = () => controller.abort();
  init?.signal?.addEventListener("abort", abortFromCaller, { once: true });

  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (unlockToken) {
    headers.set("X-Share-Token", unlockToken);
  }

  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, {
      credentials: "same-origin",
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
    throw new ApiRequestError(t("api.networkError"), "NETWORK_ERROR");
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
    throw new ApiRequestError(payload.error.message, payload.error.code);
  }
  return payload.data;
}

function buildShareTokenQuery(unlockToken = ""): string {
  return unlockToken ? `?st=${encodeURIComponent(unlockToken)}` : "";
}

export const getPublicShare = (code: string, unlockToken = "", silent = false) =>
  publicRequest<PublicShareView>(
    `/api/public/shares/${encodeURIComponent(code)}${silent ? "?silent=1" : ""}`,
    undefined,
    unlockToken
  );

export const unlockPublicShare = (code: string, passcode: string) =>
  publicRequest<PublicShareView>(`/api/public/shares/${encodeURIComponent(code)}/unlock`, {
    method: "POST",
    body: JSON.stringify({ passcode })
  });

export const getPublicShareItemPlayback = (code: string, itemId: string, unlockToken = "") =>
  publicRequest<DownloadPlaybackInfo>(
    `/api/public/shares/${encodeURIComponent(code)}/items/${encodeURIComponent(itemId)}/playback`,
    undefined,
    unlockToken,
    PLAYBACK_REQUEST_TIMEOUT_MS
  );

export const getPublicShareItemProgress = (code: string, itemId: string, unlockToken = "") =>
  publicRequest<{ bufferedSeconds?: number | null; producedSeconds?: number | null; finished: boolean; error?: string | null }>(
    `/api/public/shares/${encodeURIComponent(code)}/items/${encodeURIComponent(itemId)}/playback/progress`,
    undefined,
    unlockToken
  );

export const getPublicShareStreamUrl = (code: string, itemId: string, unlockToken = "") =>
  `${API_BASE_URL}/api/public/shares/${encodeURIComponent(code)}/items/${encodeURIComponent(itemId)}/stream${buildShareTokenQuery(unlockToken)}`;

export const getPublicShareHlsUrl = (code: string, itemId: string, unlockToken = "") =>
  `${API_BASE_URL}/api/public/shares/${encodeURIComponent(code)}/items/${encodeURIComponent(itemId)}/hls/master.m3u8${buildShareTokenQuery(unlockToken)}`;

export const getPublicShareSubtitleUrl = (code: string, itemId: string, trackId: string, unlockToken = "") =>
  `${API_BASE_URL}/api/public/shares/${encodeURIComponent(code)}/items/${encodeURIComponent(itemId)}/subtitles/${encodeURIComponent(trackId)}.vtt${buildShareTokenQuery(unlockToken)}`;

export const getPublicShareAssSubtitleUrl = (code: string, itemId: string, trackId: string, unlockToken = "") =>
  `${API_BASE_URL}/api/public/shares/${encodeURIComponent(code)}/items/${encodeURIComponent(itemId)}/subtitles/${encodeURIComponent(trackId)}.ass${buildShareTokenQuery(unlockToken)}`;

export const getPublicShareDownloadUrl = (code: string, itemId: string, unlockToken = "") =>
  `${API_BASE_URL}/api/public/shares/${encodeURIComponent(code)}/items/${encodeURIComponent(itemId)}/download${buildShareTokenQuery(unlockToken)}`;

export function resolveMangaImageUrl(url: string): string {
  if (!url) {
    return "";
  }
  const token = readApiToken();
  const base = url.startsWith("http") ? url : `${API_BASE_URL}${url}`;
  if (!token) {
    return base;
  }
  const sep = base.includes("?") ? "&" : "?";
  return `${base}${sep}token=${encodeURIComponent(token)}`;
}

export async function discoverManga(params: {
  q?: string;
  sort?: "latest" | "popular";
  lang?: "zh" | "all";
  limit?: number;
  offset?: number;
}): Promise<MangaDiscoverResult> {
  const qs = new URLSearchParams();
  if (params.q?.trim()) qs.set("q", params.q.trim());
  if (params.sort) qs.set("sort", params.sort);
  if (params.lang) qs.set("lang", params.lang);
  if (params.limit) qs.set("limit", String(params.limit));
  if (params.offset) qs.set("offset", String(params.offset));
  return request<MangaDiscoverResult>(`/api/manga/discover?${qs.toString()}`, undefined, HOMEPAGE_REQUEST_TIMEOUT_MS);
}

export async function getMangaDetail(id: string, lang: "zh" | "all" = "zh"): Promise<MangaDetailResult> {
  return request<MangaDetailResult>(
    `/api/manga/${encodeURIComponent(id)}?lang=${encodeURIComponent(lang)}`,
    undefined,
    HOMEPAGE_REQUEST_TIMEOUT_MS
  );
}

export async function getMangaChapterPages(chapterId: string): Promise<MangaPagesResult> {
  return request<MangaPagesResult>(
    `/api/manga/chapters/${encodeURIComponent(chapterId)}/pages`,
    undefined,
    HOMEPAGE_REQUEST_TIMEOUT_MS
  );
}

export async function listMangaProgress(): Promise<MangaReadProgress[]> {
  return request<MangaReadProgress[]>("/api/manga/progress");
}

export async function saveMangaProgress(payload: {
  mangaId: string;
  title: string;
  coverUrl: string;
  chapterId: string;
  chapterLabel: string;
  pageIndex: number;
  totalPages: number;
}): Promise<MangaReadProgress> {
  return request<MangaReadProgress>("/api/manga/progress", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function deleteMangaProgress(mangaId: string): Promise<{ deleted: boolean }> {
  return request<{ deleted: boolean }>(`/api/manga/progress/${encodeURIComponent(mangaId)}`, {
    method: "DELETE"
  });
}

export interface SyncPublicTrackersResult {
  btTrackers: string[];
  totalCount: number;
  addedCount: number;
  syncedCount: number;
}

export async function syncPublicTrackers(): Promise<SyncPublicTrackersResult> {
  return request<SyncPublicTrackersResult>(
    "/api/settings/trackers/sync",
    { method: "POST", body: JSON.stringify({}) },
    20000
  );
}

/** 边下边播：让任务立即开始（不排队）并优先下载媒体开头片段。 */
export async function streamDownloadNow(gid: string): Promise<DownloadJob | null> {
  return request<DownloadJob | null>(`/api/downloads/${encodeURIComponent(gid)}/stream-now`, {
    method: "POST"
  });
}
