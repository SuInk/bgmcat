import { computed, reactive, ref } from "vue";

import type {
  AppSettings,
  AuthLoginResult,
  AuthSessionSummary,
  AuthStatusResponse,
  PublicSystemStatus,
  SystemStatus
} from "@shared/index";
import { DEFAULT_THEME_ACCENT } from "@shared/index";

import { syncThemeAccent } from "./theme";

import {
  changeAdminCredentials,
  clearApiToken,
  dianaPasswordLogin,
  exchangeOAuthLogin,
  getAuthStatus,
  getCloudStatus,
  getPublicSystemStatus,
  getSettings,
  getSystemStatus,
  githubLoginUrl,
  googleLoginUrl,
  isBackendUnreachable,
  isDesktopShellRuntime,
  listAuthSessions,
  logoutSession,
  oidcLoginUrl,
  passwordLogin,
  persistApiToken,
  readApiToken,
  readDesktopShellToken,
  revokeAuthSession,
  revokeOtherAuthSessions,
  rotateApiToken,
  tokenLogin,
  updateSettings
} from "../api";
import { t } from "../i18n";
import { isBusy, withBusy } from "./busy";
import { toastError, updateFeedback } from "./toasts";

export const isAuthenticated = ref(false);
export const authChecked = ref(false);
export const adminUsername = ref("");
export const passwordConfigured = ref(true);
export const authMethod = ref("");
export const status = ref<SystemStatus | null>(null);
export const settings = ref<AppSettings | null>(null);
export const loginToken = ref(readApiToken());

// 只认自家 Electron 壳：preload 注入的 token 桥，或壳追加的 UA 标识；
// 不能笼统匹配 Electron，否则任何 Electron 内核的浏览器都会被当成桌面端而跳过登录界面
export const isDesktopShell = isDesktopShellRuntime();
export const hasConsoleAccess = computed(() => isDesktopShell || isAuthenticated.value);

export const settingsForm = reactive<AppSettings>({
 dianaMcpConfigPath: "",
  bindHost: "127.0.0.1",
  port: 3000,
  lanAccessEnabled: false,
  token: "",
  corsOrigins: [],
  pollIntervalMinutes: 30,
  downloadDir: "",
  downloadFolderMode: "anime_subgroup",
  cloudProvider: "google",
  cloudArchiveEnabled: false,
  googleClientId: "",
  googleClientSecret: "",
  cloudDriveFolderId: "root",
  googleRedirectUri: "",
  onedriveClientId: "",
  onedriveClientSecret: "",
  onedriveRedirectUri: "",
  onedriveFolderId: "root",
  webdavUrl: "",
  webdavUsername: "",
  webdavPassword: "",
  webdavRootPath: "Bangumi Cat",
  cloudDeleteLocal: false,
  downloadClient: "native",
  aria2RpcUrl: "http://127.0.0.1:6800/jsonrpc",
  aria2RpcHost: "127.0.0.1",
  aria2RpcPort: 16800,
  aria2RpcSecret: "",
  aria2SessionPath: "",
  aria2Enabled: true,
  aria2SeedEnabled: false,
  btTrackers: [],
  defaultSaveTo: "local",
  qbittorrentBaseUrl: "http://127.0.0.1:8080",
  qbittorrentUsername: "admin",
  qbittorrentPassword: "",
  qbittorrentEnabled: false,
  transmissionRpcUrl: "http://127.0.0.1:9091/transmission/rpc",
  transmissionUsername: "",
  transmissionPassword: "",
  mikanBaseUrl: "https://mikanani.me",
  providerMirrors: {},
  mikanDirectAccessMode: "prefer",
  mikanDirectAccessEnabled: true,
  mikanResolverIps: [],
  localAiEnabled: false,
  localAiBaseUrl: "http://127.0.0.1:11434",
  localAiApiKey: "",
  localAiModel: "gemma3:4b",
  localAiSystemPrompt: "",
  homepageSortMode: "default",
  multiSidebarEnabled: true,
  tunnelEnabled: false,
  tunnelMode: "quick",
  tunnelBinaryPath: "",
  tunnelAutoRestart: true,
  tunnelHostname: "",
  tunnelCredentialMode: "credentials-file",
  tunnelCredentialsFile: "",
  tunnelId: "",
  tunnelToken: "",
  exposeMcpPublicly: false,
  githubOAuthEnabled: false,
  githubClientId: "",
  githubClientSecret: "",
  githubAllowedLogins: [],
  googleOAuthEnabled: false,
  googleAuthClientId: "",
  googleAuthClientSecret: "",
  googleAllowedEmails: [],
  dianaAuthEnabled: false,
  dianaAuthBaseUrl: "",
  oidcEnabled: false,
  oidcDisplayName: "SSO",
  oidcIssuerUrl: "",
  oidcClientId: "",
  oidcClientSecret: "",
  oidcAllowedUsers: [],
  ffmpegPath: "",
  updateAutoDownload: true,
  updateAutoInstall: false,
  updateMirror: "auto",
  updateChannel: "stable",
  launchAtLogin: false,
  minimizeToTray: true,
  enabledResourceProviders: ["mikan", "animegarden", "dmhy", "bangumimoe", "acgnx", "acgrip", "nyaa"],
  resourceProviderOrder: [
    "mikan",
    "animegarden",
    "dmhy",
    "bangumimoe",
    "acgnx",
    "acgrip",
    "nyaa",
    "kisssub",
    "comicat",
    "tokyotosho"
  ],
  enabledMetadataPlatforms: ["bangumi", "anilist"],
  tmdbApiKey: "",
  themeAccent: DEFAULT_THEME_ACCENT
});

function normalizeDownloadClient(value: string | undefined): AppSettings["downloadClient"] {
  if (value === "qbittorrent" || value === "aria2-rpc" || value === "transmission") {
    return value;
  }
  return "native";
}

function normalizeCloudProvider(value: string | undefined): AppSettings["cloudProvider"] {
  if (value === "onedrive" || value === "webdav") {
    return value;
  }
  return "google";
}

function cloneSettingValue<T>(value: T): T {
  if (Array.isArray(value)) {
    return [...value] as unknown as T;
  }
  if (value && typeof value === "object") {
    return { ...(value as Record<string, unknown>) } as unknown as T;
  }
  return value;
}

export function syncSettingsForm(next: AppSettings): void {
  Object.assign(settingsForm, next, {
    downloadClient: normalizeDownloadClient(next.downloadClient),
    cloudProvider: normalizeCloudProvider(next.cloudProvider),
    corsOrigins: [...next.corsOrigins],
    mikanResolverIps: [...next.mikanResolverIps],
    btTrackers: [...(next.btTrackers ?? [])],
    githubAllowedLogins: [...(next.githubAllowedLogins ?? [])],
    googleAllowedEmails: [...(next.googleAllowedEmails ?? [])],
    oidcAllowedUsers: [...(next.oidcAllowedUsers ?? [])],
    providerMirrors: { ...(next.providerMirrors ?? {}) }
  });
  // 设置是主题色的真身：拿到就认账，顺手写进本地缓存给下次冷启动用
  syncThemeAccent(next.themeAccent);
}

export async function refreshStatus(): Promise<void> {
  status.value = await getSystemStatus();
}

export const cloudDriveConnected = ref(false);
export const cloudDriveConfigured = ref(false);

export async function refreshCloudStatus(): Promise<void> {
  try {
    const res = await getCloudStatus();
    cloudDriveConnected.value = Boolean(res.connected);
    cloudDriveConfigured.value = Boolean(res.configured);
  } catch {
    cloudDriveConnected.value = false;
  }
}

export async function refreshSettings(): Promise<void> {
  const next = await getSettings();
  settings.value = next;
  syncSettingsForm(next);
  void refreshCloudStatus();
}

export async function loginWithPassword(username: string, password: string): Promise<void> {
  const result = await passwordLogin(username.trim(), password);
  authCallbackError.value = "";
  prefillPassword.value = "";
  clearApiToken();
  loginToken.value = readDesktopShellToken();
  adminUsername.value = result.username;
  passwordConfigured.value = true;
  authMethod.value = "password";
  status.value = result.status;
  isAuthenticated.value = true;
}

export async function login(): Promise<boolean> {
  return withBusy("login", async () => {
    try {
      const nextStatus = await tokenLogin(loginToken.value.trim());
      persistApiToken(loginToken.value.trim());
      status.value = nextStatus;
      isAuthenticated.value = true;
      updateFeedback(t("auth.tokenAccepted"), "success");
      return true;
    } catch (error) {
      toastError(error, t("auth.loginFailed"));
      return false;
    }
  });
}

export async function logout(): Promise<void> {
  try {
    await logoutSession();
  } catch {
    // 登出失败(离线、会话已失效)也要让本地退出。
  }
  clearApiToken();
  prefillPassword.value = "";
  loginToken.value = readDesktopShellToken();
  isAuthenticated.value = false;
  adminUsername.value = "";
  authMethod.value = "";
  authSessions.value = [];
  updateFeedback(t("auth.tokenCleared"), "info");
}

/** 未鉴权时也能读到的公开状态，用来决定会话是否有效以及是否显示第三方/快捷登录入口。 */
export const githubOAuthAvailable = ref(false);
export const googleOAuthAvailable = ref(false);
export const dianaAuthAvailable = ref(false);
export const dianaOwnerPairAvailable = ref(false);
export const oidcAvailable = ref(false);
export const oidcDisplayName = ref("SSO");
export const authCallbackError = ref("");
export const prefillUsername = ref("");
export const prefillPassword = ref("");
export const recentSavedPassword = ref("");

function applyPublicAuthFlags(flags: Partial<PublicSystemStatus | AuthStatusResponse> | null): void {
  githubOAuthAvailable.value = Boolean(flags?.githubOAuthAvailable);
  googleOAuthAvailable.value = Boolean(flags?.googleOAuthAvailable);
  dianaAuthAvailable.value = Boolean(flags?.dianaAuthAvailable);
  dianaOwnerPairAvailable.value = Boolean(flags?.dianaOwnerPairAvailable);
  oidcAvailable.value = Boolean(flags?.oidcAvailable);
  oidcDisplayName.value = flags?.oidcDisplayName || "SSO";
}

export function hydrateSessionFromBootstrap(
  auth: AuthStatusResponse,
  nextStatus?: SystemStatus | null,
  nextSettings?: AppSettings | null
): boolean {
  applyPublicAuthFlags(auth);
  passwordConfigured.value = auth.passwordConfigured;
  authMethod.value = auth.authMethod;
  if (auth.authenticated) {
    adminUsername.value = auth.username;
    isAuthenticated.value = true;
    if (nextStatus) {
      status.value = nextStatus;
      cloudDriveConnected.value = Boolean(nextStatus.cloudConnected);
    }
    if (nextSettings) {
      settings.value = nextSettings;
      syncSettingsForm(nextSettings);
    }
    return true;
  }
  if (!isDesktopShell) {
    isAuthenticated.value = false;
    adminUsername.value = "";
  }
  return false;
}

export async function refreshAuthStatus(options: { throwOnUnreachable?: boolean } = {}): Promise<boolean> {
  try {
    const res = await getAuthStatus();
    applyPublicAuthFlags(res);
    passwordConfigured.value = res.passwordConfigured;
    authMethod.value = res.authMethod;
    if (res.authenticated) {
      adminUsername.value = res.username;
      isAuthenticated.value = true;
      return true;
    }
    if (!isDesktopShell) {
      isAuthenticated.value = false;
      adminUsername.value = "";
    }
    return false;
  } catch (error) {
    if (options.throwOnUnreachable && isBackendUnreachable(error)) {
      throw error;
    }
    await refreshPublicStatus();
    return false;
  }
}

export async function refreshPublicStatus(): Promise<void> {
  try {
    applyPublicAuthFlags(await getPublicSystemStatus());
  } catch {
    applyPublicAuthFlags(null);
  }
}

export function startGithubLogin(): void {
  window.location.href = githubLoginUrl();
}

export function startGoogleLogin(): void {
  window.location.href = googleLoginUrl();
}

export function startOidcLogin(): void {
  window.location.href = oidcLoginUrl();
}

export async function loginWithDianaPassword(username: string, password: string): Promise<void> {
  const result = await dianaPasswordLogin(username.trim(), password);
  completeDianaSessionResult(result);
}

export function completeDianaSessionResult(result: Partial<AuthLoginResult>): void {
  authCallbackError.value = "";
  clearApiToken();
  prefillPassword.value = "";
  loginToken.value = readDesktopShellToken();
  if (result.username) {
    adminUsername.value = result.username;
  }
  authMethod.value = "diana";
  if (result.status) {
    status.value = result.status;
  }
  isAuthenticated.value = true;
}

const OAUTH_METHOD_LABELS = (): Record<string, string> => ({
  github: "GitHub",
  google: "Google",
  oidc: oidcDisplayName.value || "SSO",
  diana: "Diana",
  qrcode: "扫码登录"
});

/** 用 OAuth 回调带回来的一次性 code 换 HttpOnly 会话 Cookie；成功后当前浏览器即完成登录。 */
export async function completeOAuthLogin(code: string): Promise<boolean> {
  try {
    const result = await exchangeOAuthLogin(code);
    const method = result.method || "github";
    const label = OAUTH_METHOD_LABELS()[method] || method;
    authCallbackError.value = "";
    clearApiToken();
    loginToken.value = readDesktopShellToken();
    isAuthenticated.value = true;
    adminUsername.value = result.login;
    authMethod.value = method;
    updateFeedback(`已通过 ${label} 账号 ${result.login} 登录。`, "success");
    return true;
  } catch (error) {
    toastError(error, "第三方登录失败。");
    return false;
  }
}

export const completeGithubLogin = completeOAuthLogin;

export const authSessions = ref<AuthSessionSummary[]>([]);
export const githubSessions = computed(() =>
  authSessions.value.filter(
    (item) =>
      item.authMethod === "github" ||
      item.authMethod === "google" ||
      item.authMethod === "oidc" ||
      item.authMethod === "diana" ||
      item.authMethod === "qrcode" ||
      Boolean(item.githubLogin)
  )
);
export const otherSessionCount = computed(() => authSessions.value.filter((item) => !item.current).length);
export const loadingSessions = computed(() => isBusy("auth-sessions"));
export const savingCredentials = computed(() => isBusy("save-credentials"));

export async function saveAdminCredentials(payload: {
  newUsername: string;
  newPassword: string;
}): Promise<boolean> {
  return withBusy("save-credentials", async () => {
    try {
      const wasConfigured = passwordConfigured.value;
      const result = await changeAdminCredentials(payload);
      adminUsername.value = result.username;
      recentSavedPassword.value = payload.newPassword;
      passwordConfigured.value = true;
      void refreshAuthSessions();
      updateFeedback(wasConfigured ? "管理员账号与密码已更新。" : "管理员密码已设置。", "success");
      return true;
    } catch (error) {
      toastError(error, "更新账号与密码失败。");
      return false;
    }
  });
}

export async function refreshAuthSessions(): Promise<void> {
  await withBusy("auth-sessions", async () => {
    try {
      authSessions.value = await listAuthSessions();
    } catch {
      authSessions.value = [];
    }
  });
}

export async function revokeSession(id: number): Promise<void> {
  await withBusy(`auth-session:${id}`, async () => {
    try {
      const result = await revokeAuthSession(id);
      if (result.current && !isDesktopShell) {
        await logout();
        return;
      }
      await refreshAuthSessions();
      updateFeedback("该设备已登出。", "success");
    } catch (error) {
      toastError(error, "撤销登录会话失败。");
    }
  });
}

export async function revokeOtherSessions(): Promise<void> {
  await withBusy("auth-session:others", async () => {
    try {
      const result = await revokeOtherAuthSessions();
      await refreshAuthSessions();
      updateFeedback(`已登出其他 ${result.revoked} 个设备。`, "success");
    } catch (error) {
      toastError(error, "登出其他设备失败。");
    }
  });
}

export const rotatingToken = computed(() => isBusy("rotate-token"));

/** 重新生成 API / MCP 静态令牌；若当前用的是静态令牌则同步刷新本地缓存。 */
export async function rotateToken(): Promise<void> {
  await withBusy("rotate-token", async () => {
    try {
      const hadStoredToken = Boolean(readApiToken() && !readDesktopShellToken());
      const next = await rotateApiToken();
      settings.value = next;
      syncSettingsForm(next);
      if (hadStoredToken) {
        persistApiToken(next.token);
        loginToken.value = next.token;
      }
      updateFeedback(t("settings.saved"), "success");
    } catch (error) {
      toastError(error, t("settings.saveFailed"));
    }
  });
}

/* —— 自动保存 ——
   设置页不再有「保存修改」按钮。开关/分段/预设改完立刻提交；文本框与数字框
   失焦或回车才提交（把端口删空再打新值的中间态一旦落库就是事故）。

   只提交改动的那几个字段，不整表 PUT：整表会把别的页面/别的窗口刚改的字段
   用自己这份旧快照覆盖回去。请求也串行化，避免后发的先到。 */

export type SettingsSaveState = "idle" | "saving" | "saved" | "error";

export const settingsSaveState = ref<SettingsSaveState>("idle");
export const settingsSaveError = ref<string | null>(null);
/** 字段级校验错误，键是字段名；设置页就地标红用。 */
export const settingsFieldErrors = reactive<Record<string, string>>({});

/** 上一次提交失败的字段，供「重试」按钮再来一次。 */
const failedKeys = new Set<keyof AppSettings>();
export const settingsRetryable = computed(() => settingsSaveState.value === "error" && failedKeys.size > 0);

let saveQueue: Promise<unknown> = Promise.resolve();
/** 已入队待发的字段。表单的 submit 与 keydown.enter 会在同一 tick 各触发一次，靠这个去重。 */
const queuedKeys = new Set<keyof AppSettings>();
let savedFlashTimer: ReturnType<typeof setTimeout> | null = null;

function buildPatch(keys: ReadonlyArray<keyof AppSettings>): Partial<AppSettings> {
  const patch: Record<string, unknown> = {};
  for (const key of keys) {
    patch[key] = cloneSettingValue(settingsForm[key]);
  }
  return patch as Partial<AppSettings>;
}

function flashSaved(): void {
  settingsSaveState.value = "saved";
  if (savedFlashTimer) {
    clearTimeout(savedFlashTimer);
  }
  savedFlashTimer = setTimeout(() => {
    if (settingsSaveState.value === "saved") {
      settingsSaveState.value = "idle";
    }
    savedFlashTimer = null;
  }, 2200);
}

/** 成功后只把「刚存的这几个字段」回写进表单，别整表覆盖——用户可能正在另一个框里打字。 */
function acceptSaved(next: AppSettings, keys: ReadonlyArray<keyof AppSettings>): void {
  settings.value = next;
  for (const key of keys) {
    (settingsForm as Record<string, unknown>)[key] = cloneSettingValue(next[key]);
  }
  syncThemeAccent(next.themeAccent);
}

/** 提交给定字段。非法值就地标红、不提交，也不静默丢弃。 */
export async function saveSettingsFields(inputKeys: ReadonlyArray<keyof AppSettings>): Promise<void> {
  let keys = inputKeys;
  if (!keys.length) {
    return saveQueue.then(() => undefined);
  }

  for (const key of keys) {
    delete settingsFieldErrors[key as string];
  }

  const fresh = keys.filter((key) => !queuedKeys.has(key));
  if (!fresh.length) {
    return saveQueue.then(() => undefined);
  }
  keys = fresh;

  const { updateSettingsSchema } = await import("@shared/schemas");
  const parsed = updateSettingsSchema.safeParse(buildPatch(keys));
  if (!parsed.success) {
    for (const issue of parsed.error.issues) {
      const field = String(issue.path[0] ?? "");
      if (field) {
        settingsFieldErrors[field] = issue.message;
      }
    }
    settingsSaveState.value = "error";
    settingsSaveError.value = "有字段填得不对，这次没有保存。";
    return;
  }

  for (const key of keys) {
    queuedKeys.add(key);
  }

  const run = async (): Promise<void> => {
    for (const key of keys) {
      queuedKeys.delete(key);
    }
    settingsSaveState.value = "saving";
    settingsSaveError.value = null;
    try {
      // 排队期间用户可能又改了同一个字段，这里用最新快照重建一次
      const next = await updateSettings(buildPatch(keys));
      acceptSaved(next, keys);
      for (const key of keys) {
        failedKeys.delete(key);
      }
      await refreshStatus().catch(() => undefined);
      flashSaved();
    } catch (error) {
      for (const key of keys) {
        failedKeys.add(key);
      }
      settingsSaveState.value = "error";
      settingsSaveError.value = error instanceof Error ? error.message : String(error);
    }
  };

  saveQueue = saveQueue.then(run, run);
  return saveQueue.then(() => undefined);
}

export async function retrySettingsSave(): Promise<void> {
  const keys = [...failedKeys];
  if (!keys.length) {
    return;
  }
  await saveSettingsFields(keys);
}

/** 拿当前表单和已存快照做差，返回 keys 里真正变了的那些。 */
export function changedSettingKeys(keys: ReadonlyArray<keyof AppSettings>): Array<keyof AppSettings> {
  const saved = settings.value;
  if (!saved) {
    return [];
  }
  return keys.filter((key) => {
    const formValue = settingsForm[key];
    const savedValue = saved[key];
    if (Array.isArray(formValue) && Array.isArray(savedValue)) {
      return formValue.join("\u0000") !== savedValue.join("\u0000");
    }
    if (formValue && savedValue && typeof formValue === "object" && typeof savedValue === "object") {
      return JSON.stringify(formValue) !== JSON.stringify(savedValue);
    }
    return formValue !== savedValue;
  });
}

