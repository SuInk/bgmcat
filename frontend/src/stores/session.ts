import { computed, reactive, ref } from "vue";

import type { AppSettings, AuthSessionSummary, SystemStatus } from "@shared/index";
import { DEFAULT_THEME_ACCENT, updateSettingsSchema } from "@shared/index";

import { syncThemeAccent } from "./theme";

import {
  clearApiToken,
  exchangeGithubLogin,
  getPublicSystemStatus,
  getSettings,
  getSystemStatus,
  githubLoginUrl,
  isDesktopShellRuntime,
  listAuthSessions,
  logoutSession,
  persistApiToken,
  readApiToken,
  revokeAuthSession,
  rotateApiToken,
  tokenLogin,
  updateSettings
} from "../api";
import { t } from "../i18n";
import { isBusy, withBusy } from "./busy";
import { toastError, updateFeedback } from "./toasts";

export const isAuthenticated = ref(false);
export const authChecked = ref(false);
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
  token: "",
  corsOrigins: [],
  pollIntervalMinutes: 30,
  downloadDir: "",
    cloudArchiveEnabled: false,
    googleClientId: "",
    googleClientSecret: "",
    cloudDriveFolderId: "root",
    googleRedirectUri: "",
    cloudDeleteLocal: false,
  downloadClient: "native",
  aria2RpcHost: "127.0.0.1",
  aria2RpcPort: 16800,
  aria2RpcSecret: "",
  aria2SessionPath: "",
  aria2Enabled: true,
  aria2SeedEnabled: false,
  qbittorrentBaseUrl: "http://127.0.0.1:8080",
  qbittorrentUsername: "admin",
  qbittorrentPassword: "",
  qbittorrentEnabled: false,
  mikanBaseUrl: "https://mikanime.tv",
  mikanDirectAccessMode: "prefer",
  mikanDirectAccessEnabled: true,
  mikanResolverIps: [],
  localAiEnabled: false,
  localAiBaseUrl: "http://127.0.0.1:11434",
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
  ffmpegPath: "",
  updateAutoDownload: true,
  updateAutoInstall: false,
  updateMirror: "auto",
  updateChannel: "stable",
  launchAtLogin: false,
  minimizeToTray: true,
  enabledResourceProviders: ["mikan"],
  resourceProviderOrder: ["mikan", "acgrip", "nyaa"],
  enabledMetadataPlatforms: ["bangumi", "anilist"],
  tmdbApiKey: "",
  themeAccent: DEFAULT_THEME_ACCENT
});

export function syncSettingsForm(next: AppSettings): void {
  Object.assign(settingsForm, next, {
    corsOrigins: [...next.corsOrigins],
    mikanResolverIps: [...next.mikanResolverIps]
  });
  // 设置是主题色的真身：拿到就认账，顺手写进本地缓存给下次冷启动用
  syncThemeAccent(next.themeAccent);
}

export async function refreshStatus(): Promise<void> {
  status.value = await getSystemStatus();
}

export async function refreshSettings(): Promise<void> {
  const next = await getSettings();
  settings.value = next;
  syncSettingsForm(next);
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
  // 先让服务端撤销 GitHub 会话（静态 token 登录时是个 no-op），再清本地。
  try {
    await logoutSession();
  } catch {
    // 登出失败(离线、token 已失效)也要让本地退出。
  }
  clearApiToken();
  loginToken.value = "";
  isAuthenticated.value = false;
  githubSessions.value = [];
  updateFeedback(t("auth.tokenCleared"), "info");
}

/** 未鉴权时也能读到的公开状态，只用来决定要不要显示 GitHub 登录按钮。 */
export const githubOAuthAvailable = ref(false);

export async function refreshPublicStatus(): Promise<void> {
  try {
    githubOAuthAvailable.value = (await getPublicSystemStatus()).githubOAuthAvailable;
  } catch {
    githubOAuthAvailable.value = false;
  }
}

export function startGithubLogin(): void {
  window.location.href = githubLoginUrl();
}

/** 用 GitHub 回调带回来的一次性 code 换 token；成功后当前浏览器就算登录了。 */
export async function completeGithubLogin(code: string): Promise<boolean> {
  try {
    const result = await exchangeGithubLogin(code);
    persistApiToken(result.token);
    loginToken.value = result.token;
    updateFeedback(`已通过 GitHub 账号 ${result.login} 登录。`, "success");
    return true;
  } catch (error) {
    toastError(error, "GitHub 登录失败。");
    return false;
  }
}

export const githubSessions = ref<AuthSessionSummary[]>([]);
export const loadingSessions = computed(() => isBusy("auth-sessions"));

export async function refreshAuthSessions(): Promise<void> {
  await withBusy("auth-sessions", async () => {
    try {
      githubSessions.value = await listAuthSessions();
    } catch {
      githubSessions.value = [];
    }
  });
}

export async function revokeSession(id: number): Promise<void> {
  await withBusy(`auth-session:${id}`, async () => {
    try {
      await revokeAuthSession(id);
      await refreshAuthSessions();
      updateFeedback("已撤销该登录会话。", "success");
    } catch (error) {
      toastError(error, "撤销登录会话失败。");
    }
  });
}

export const rotatingToken = computed(() => isBusy("rotate-token"));

/** 重新生成静态 token；当前会话立即换用新 token，避免自己把自己踢下线。 */
export async function rotateToken(): Promise<void> {
  await withBusy("rotate-token", async () => {
    try {
      const next = await rotateApiToken();
      settings.value = next;
      syncSettingsForm(next);
      persistApiToken(next.token);
      loginToken.value = next.token;
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
    const value = settingsForm[key];
    patch[key] = Array.isArray(value) ? [...value] : value;
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
    const value = next[key];
    (settingsForm as Record<string, unknown>)[key] = Array.isArray(value) ? [...value] : value;
  }
  syncThemeAccent(next.themeAccent);
}

/** 提交给定字段。非法值就地标红、不提交，也不静默丢弃。 */
export function saveSettingsFields(inputKeys: ReadonlyArray<keyof AppSettings>): Promise<void> {
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
    return Promise.resolve();
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
      return formValue.join(" ") !== savedValue.join(" ");
    }
    return formValue !== savedValue;
  });
}

