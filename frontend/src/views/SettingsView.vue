<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import type { AppSettings } from "@shared/index";
import { DEFAULT_THEME_ACCENT } from "@shared/index";

import {
  getCloudStatus, connectCloud, disconnectCloud,
  getDriveNetwork, setDriveNetwork, scanDriveNetwork,
  clearPlaybackCache,
  createBackup,
  deleteBackup,
  downloadBackup,
  importBackup,
  listBackups,
  listMetadataPlatforms,
  listResourceProviders,
  openDataDir,
  restoreBackup,
  testMetadataPlatform,
  testResourceProvider
} from "../api";
import type { DriveNetworkStatus, MetadataPlatformInfo } from "../api";
import type { BackupSummary, ResourceProviderSummary } from "@shared/index";
import VersionPanel from "../components/settings/VersionPanel.vue";
import CloudSetup from "../components/settings/CloudSetup.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { formatBytes } from "../lib/format";
import { copyText, openExternalLink } from "../lib/actions";
import { isBusy, withBusy } from "../stores/busy";
import {
  addTunnelOriginToCors,
  applyDiscoveredTunnel,
  discoveredTunnels,
  refreshDiscoveredTunnels,
  refreshTunnel,
  refreshTunnelLogs,
  restartTunnelAction,
  setTunnelEnabled,
  tunnelBlockers,
  tunnelBusy,
  tunnelLogs,
  tunnelStateLabel,
  tunnelStatus,
  tunnelWarnings
} from "../stores/tunnel";
import {
  changedSettingKeys,
  githubSessions,
  loadingSessions,
  refreshAuthSessions,
  retrySettingsSave,
  revokeSession,
  rotateToken,
  rotatingToken,
  saveSettingsFields,
  settings,
  settingsFieldErrors,
  settingsForm,
  settingsRetryable,
  settingsSaveError,
  settingsSaveState,
  status
} from "../stores/session";

const driveNetwork = ref<DriveNetworkStatus | null>(null);
const driveCandidates = ref("");
const driveSNIMode = ref<DriveNetworkStatus["sniMode"]>("original");
const driveSNIHost = ref("");
function applyDriveNetwork(value: DriveNetworkStatus) {
  driveNetwork.value = value;
  driveSNIMode.value = value.sniMode;
  driveSNIHost.value = value.sniHost;
}
async function saveDriveTLS() {
  driveNetworkSaving.value = true;
  try { applyDriveNetwork(await setDriveNetwork({ sniMode: driveSNIMode.value, sniHost: driveSNIHost.value })); }
  catch (error) { toastError(error, "保存 SNI 设置失败"); }
  finally { driveNetworkSaving.value = false; }
}
const driveScanning = ref(false);
const driveNetworkSaving = ref(false);
async function refreshDriveNetwork() {
  try { applyDriveNetwork(await getDriveNetwork()); } catch (error) { toastError(error, "读取云盘网络状态失败"); }
}
async function toggleDriveNetwork(event: Event) {
  const input = event.target as HTMLInputElement;
  driveNetworkSaving.value = true;
  try { driveNetwork.value = await setDriveNetwork({ enabled: input.checked }); }
  catch (error) { input.checked = driveNetwork.value?.enabled ?? false; toastError(error, "保存云盘网络设置失败"); }
  finally { driveNetworkSaving.value = false; }
}
async function scanCloudNetwork() {
  driveScanning.value = true;
  try {
    applyDriveNetwork(await setDriveNetwork({ sniMode: driveSNIMode.value, sniHost: driveSNIHost.value }));
    driveNetwork.value = await scanDriveNetwork(driveCandidates.value);
  }
  catch (error) { toastError(error, "云盘 IP 扫描失败"); }
  finally { driveScanning.value = false; }
}
onMounted(() => { void refreshDriveNetwork(); });

const cloudSetup = ref<InstanceType<typeof CloudSetup> | null>(null);
const cloudConnected = ref(false);
async function refreshCloud() {
  try { cloudConnected.value = (await getCloudStatus()).connected; } catch { /* login handles auth */ }
}
async function authorizeCloud() {
  try {
    if (cloudConnected.value) { await disconnectCloud(); settingsForm.cloudArchiveEnabled = false; await refreshCloud(); }
    else { await saveSettingsFields(["googleClientId", "googleClientSecret", "googleRedirectUri"]); const result = await connectCloud(); await openExternalLink(result.url); }
  } catch (error) { toastError(error, "云盘连接失败"); }
}
onMounted(() => { void refreshCloud(); window.addEventListener("focus", refreshCloud); });
onUnmounted(() => window.removeEventListener("focus", refreshCloud));

/**
 * 这个字段是不是被环境变量接管了（容器部署常见）。
 * 服务端在 lockedSettings 里给出字段名；禁用输入框并说明原因，
 * 比让用户改完保存、再看着它弹回去要好。
 */
function isSettingLocked(key: string): boolean {
  return (settings.value?.lockedSettings ?? []).includes(key);
}

import { previewThemeAccent, setThemeMode, themeMode } from "../stores/theme";
import { toastError, updateFeedback } from "../stores/toasts";
// —— 外观：明暗模式 + 主题色 ——
const THEME_MODE_OPTIONS = [
  { key: "system", label: "跟随系统" },
  { key: "light", label: "浅色" },
  { key: "dark", label: "深色" }
] as const;

const ACCENT_PRESETS = [
  { label: "电光蓝", value: "#0041fd" },
  { label: "樱粉", value: "#e0578f" },
  { label: "星夜紫", value: "#7c5cff" },
  { label: "松间绿", value: "#159f6c" }
] as const;

const ACCENT_HEX_RE = /^#[0-9a-fA-F]{6}$/u;

const accentHexDraft = ref(settingsForm.themeAccent);

/**
 * 选色即时预览（只写根元素变量）。
 * commit=false 用在取色器拖动过程中：预览要跟手，但别每动一下就发一次请求。
 */
function pickAccent(value: string, commit = true): void {
  if (!ACCENT_HEX_RE.test(value.trim())) {
    return;
  }
  const next = value.trim().toLowerCase();
  settingsForm.themeAccent = next;
  accentHexDraft.value = next;
  previewThemeAccent(next);
  if (commit) {
    void saveSettingsFields(["themeAccent"]);
  }
}

/** hex 输入框失焦/回车才认；非法就退回当前值，不要边打字边闪。 */
function commitAccentHex(): void {
  const raw = accentHexDraft.value.trim();
  const normalized = raw.startsWith("#") ? raw : `#${raw}`;
  if (!ACCENT_HEX_RE.test(normalized)) {
    accentHexDraft.value = settingsForm.themeAccent;
    return;
  }
  pickAccent(normalized);
}

/* —— 自动保存 ——
   页面底部不再有「保存修改」栏。两类控件提交时机不同：

   1. 开关 / 分段 / 预设 / 下拉：一次点击就是一个完整意图，改完立刻提交。
      下面这个 watcher 盯着 IMMEDIATE_KEYS，和已存快照做差后只提交变了的字段。
   2. 文本框 / 数字框：失焦或回车才提交。用户把端口从 3002 删到空再打新值，
      那个空字符串的中间态一旦落库就是事故。走表单上的 focusout / Enter 委托。 */

const IMMEDIATE_KEYS = [
  // themeAccent 不在这里：取色器拖动时每一帧都会写它，得由 pickAccent 自己决定何时提交
  "exposeMcpPublicly",
  "githubOAuthEnabled",
  "minimizeToTray",
  "launchAtLogin",
  "downloadClient",
  "aria2Enabled",
  "aria2SeedEnabled",
  "cloudArchiveEnabled",
  "cloudDeleteLocal",
  "qbittorrentEnabled",
  "mikanDirectAccessMode",
  "mikanDirectAccessEnabled",
  "homepageSortMode",
  "tunnelMode",
  "tunnelCredentialMode",
  "tunnelAutoRestart",
  "enabledResourceProviders",
  "resourceProviderOrder"
] as const satisfies ReadonlyArray<keyof AppSettings>;

/** 其余全是自由文本/数字，只在失焦或回车时提交。 */
const DEFERRED_KEYS = (Object.keys(settingsForm) as Array<keyof AppSettings>).filter(
  (key) => !(IMMEDIATE_KEYS as ReadonlyArray<string>).includes(key)
);

watch(
  () => IMMEDIATE_KEYS.map((key) => settingsForm[key]),
  () => {
    const changed = changedSettingKeys(IMMEDIATE_KEYS);
    if (changed.length) {
      void saveSettingsFields(changed);
    }
  },
  { deep: true }
);

/** 失焦 / 回车：把文本类字段里真正改过的提交掉。 */
function commitTextFields(): void {
  const changed = changedSettingKeys(DEFERRED_KEYS);
  if (changed.length) {
    void saveSettingsFields(changed);
  }
}

const SAVE_STATE_LABEL: Record<string, string> = {
  saving: "保存中…",
  saved: "已保存 ✓",
  error: "保存失败"
};

// 设置从服务端到达（或用户点了「重置」）时，把草稿和预览拉回真身
watch(
  () => settingsForm.themeAccent,
  (next) => {
    if (next && next !== accentHexDraft.value) {
      accentHexDraft.value = next;
    }
    if (next) {
      previewThemeAccent(next);
    }
  }
);

onMounted(() => {
  void refreshAuthSessions();
  void refreshBackups();
});

onUnmounted(() => {
});

function formatSessionTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

/*
 * 设置接管整个窗口，照 ChatGPT 桌面端的设置页：
 * 左边是设置自己的侧栏（返回应用 / 搜索 / 分组的分类），右边一次只看一个分类，
 * 内容限宽居中，小节标题 + 卡片，卡片里一行一项、控件靠右。
 * 搜索时不分页，跨所有分类列出命中的设置项。
 */
type SettingsPageKey = "general" | "data" | "about" | "download" | "sources" | "metadata" | "remote" | "tunnel" | "github";

interface SettingsPage {
  key: SettingsPageKey;
  label: string;
  desc: string;
  icon: string;
}

const PAGE_GROUPS: Array<{ label: string; pages: SettingsPage[] }> = [
  {
    label: "应用",
    pages: [
      { key: "general", label: "常规", desc: "外观、刷新频率和 Go 服务。", icon: "settings" },
      { key: "data", label: "数据与备份", desc: "订阅、下载记录和设置都存在数据目录里，可以随时备份与恢复。", icon: "inbox" },
      { key: "about", label: "关于与更新", desc: "当前版本与自动更新策略。", icon: "overview" }
    ]
  },
  {
    label: "下载与资源",
    pages: [
      { key: "download", label: "下载", desc: "下载目录、下载器、Google Drive 云盘归档和在线播放。", icon: "downloads" },
      { key: "sources", label: "资源站", desc: "种子从哪些站点找，以及蜜柑计划的访问方式。", icon: "feed" },
      { key: "metadata", label: "番剧信息", desc: "番名、评分、简介和封面从哪儿来。", icon: "list" }
    ]
  },
  {
    label: "远程访问",
    pages: [
      { key: "remote", label: "访问与口令", desc: "从其他设备打开 WebUI 需要的地址、口令和网络设置。", icon: "integrations" },
      { key: "tunnel", label: "Cloudflare 隧道", desc: "不开端口、不配路由器，把 WebUI 安全地放到公网上。", icon: "external" },
      { key: "github", label: "GitHub 登录", desc: "从公网打开时，用 GitHub 账号登录代替粘贴 token。", icon: "logout" }
    ]
  }
];

const ALL_PAGES = PAGE_GROUPS.flatMap((group) => group.pages);

/** 模板里每个小节（data-section）归哪个分类页 */
const SECTION_PAGE: Record<string, SettingsPageKey> = {
  appearance: "general",
  run: "general",
  desktop: "general",
  data: "data",
  about: "about",
  download: "download",
  aria2: "download",
  qbittorrent: "download",
  playback: "download",
  sites: "sources",
  mikan: "sources",
  metadata: "metadata",
  "remote-status": "remote",
  token: "remote",
  lan: "remote",
  mcp: "remote",
  tunnel: "tunnel",
  github: "github"
};

/** 模板里的大分类块（settings-category）各含哪些分类页；搜索时才显示它的标题 */
const CATEGORY_PAGES: Record<string, SettingsPageKey[]> = {
  general: ["general", "data"],
  download: ["download"],
  sources: ["sources", "metadata"],
  remote: ["remote", "tunnel", "github"],
  about: ["about"]
};

const SETTINGS_PAGE_KEY = "anime-rss.settings.page";
/** AGPL-3.0 第 13 条：通过网络向他人提供服务时，要给出获取对应源码的途径 */
const SOURCE_REPOSITORY_URL = "https://github.com/SuInk/bgmcat";

function readStoredPage(): SettingsPageKey {
  try {
    const stored = window.localStorage.getItem(SETTINGS_PAGE_KEY) as SettingsPageKey | null;
    return stored && ALL_PAGES.some((page) => page.key === stored) ? stored : "general";
  } catch {
    return "general";
  }
}

const activePage = ref<SettingsPageKey>(readStoredPage());
const activePageMeta = computed(() => ALL_PAGES.find((page) => page.key === activePage.value) ?? ALL_PAGES[0]!);
const settingsShellRef = ref<HTMLElement | null>(null);
const settingsLayoutRef = ref<HTMLElement | null>(null);


function openPage(key: SettingsPageKey): void {
  activePage.value = key;
  settingsQuery.value = "";
  settingsLayoutRef.value?.scrollTo({ top: 0 });
  try {
    window.localStorage.setItem(SETTINGS_PAGE_KEY, key);
  } catch {
    // 存不了就每次从「常规」开始，不影响使用
  }
}

const searching = computed(() => Boolean(settingsQuery.value.trim()));

function sectionShown(id: string): boolean {
  return searching.value || SECTION_PAGE[id] === activePage.value;
}

function categoryShown(key: string): boolean {
  return searching.value || (CATEGORY_PAGES[key] ?? []).includes(activePage.value);
}

/** 只有一个小节的分类页，小节标题和页标题说的是一回事，不再重复显示 */
const singleSectionPage = computed(
  () => !searching.value && Object.values(SECTION_PAGE).filter((page) => page === activePage.value).length === 1
);

// —— 搜索 ——
// 模板是一大段手写的静态表单，逐项做成数据驱动代价太大；这里直接按 DOM 文本过滤，
// 命中的行留下，整节都没命中的小节和分类藏起来，命中项在「高级」里就顺手展开。
const settingsQuery = ref("");
const visibleSectionIds = ref<Set<string> | null>(null);

function applySettingsSearch(): void {
  const root = settingsShellRef.value;
  if (!root) {
    return;
  }
  const query = settingsQuery.value.trim().toLowerCase();
  const visible = new Set<string>();

  root.querySelectorAll<HTMLElement>("[data-section]").forEach((section) => {
    const title = section.querySelector(".settings-section-title")?.textContent?.toLowerCase() ?? "";
    const titleHit = !query || title.includes(query);
    let itemHit = false;

    section.querySelectorAll<HTMLElement>(".settings-item").forEach((item) => {
      const hit = titleHit || (item.textContent ?? "").toLowerCase().includes(query);
      item.classList.toggle("settings-search-miss", !hit);
      if (hit && query && !titleHit) {
        item.closest("details")?.setAttribute("open", "");
      }
      itemHit ||= hit;
    });
    // 小节里除了标题以外的块：卡片 / 高级折叠区一行都没命中就整块藏；
    // 隧道检查、日志、状态格这类不是设置项的块，只有按小节标题命中时才留着
    for (const child of Array.from(section.children) as HTMLElement[]) {
      if (child.classList.contains("settings-section-head")) {
        continue;
      }
      const holdsItems = child.matches(".settings-group, details") || Boolean(child.querySelector(".settings-item"));
      const miss = holdsItems
        ? Boolean(query) && !child.querySelector(".settings-item:not(.settings-search-miss)")
        : !titleHit;
      child.classList.toggle("settings-search-miss", miss);
    }

    const sectionHit = titleHit || itemHit;
    section.classList.toggle("settings-search-miss", !sectionHit);
    if (sectionHit) {
      visible.add(section.dataset.section ?? "");
    }
  });

  root.querySelectorAll<HTMLElement>(".settings-category").forEach((category) => {
    category.classList.toggle("settings-search-miss", !category.querySelector("[data-section]:not(.settings-search-miss)"));
  });

  visibleSectionIds.value = query ? visible : null;
}

watch(settingsQuery, async () => {
  // 等 v-show 把其他分类的小节放出来再过滤
  await nextTick();
  applySettingsSearch();
  settingsLayoutRef.value?.scrollTo({ top: 0 });
});

/** 搜索时侧栏每个分类后面标出命中了几节，没命中的变灰 */
const pageMatchCount = computed<Record<string, number>>(() => {
  const counts: Record<string, number> = {};
  for (const id of visibleSectionIds.value ?? []) {
    const page = SECTION_PAGE[id];
    if (page) {
      counts[page] = (counts[page] ?? 0) + 1;
    }
  }
  return counts;
});

// 这几个开关会让小节或行出现/消失，搜索中的话重新过滤一遍
watch(
  () => [settingsForm.downloadClient, settingsForm.githubOAuthEnabled, settingsForm.tunnelMode, settingsForm.tunnelCredentialMode],
  () => {
    if (searching.value) {
      void nextTick(applySettingsSearch);
    }
  }
);

onMounted(() => {
  void loadSourceLists();
  // 隧道明细只在打开设置页时拉一次：实时状态已经由 boot.ts 的 status 轮询带回来了
  void refreshTunnel();
  void refreshTunnelLogs();
  void refreshDiscoveredTunnels();
});

// —— 资源来源 ——
const resourceProviders = ref<ResourceProviderSummary[]>([]);
const providerTesting = ref<string | null>(null);
const providerResults = ref<Record<string, { ok: boolean; count: number; error: string | null }>>({});

/** 按设置里的顺序排；不在顺序列表里的排最后（新增的源不会凭空跑到最前面）。 */
const orderedProviders = computed(() => {
  const order = settingsForm.resourceProviderOrder ?? [];
  return [...resourceProviders.value].sort((left, right) => {
    const leftIndex = order.indexOf(left.id);
    const rightIndex = order.indexOf(right.id);
    return (leftIndex === -1 ? order.length : leftIndex) - (rightIndex === -1 ? order.length : rightIndex);
  });
});

function isProviderEnabled(providerId: string): boolean {
  return (settingsForm.enabledResourceProviders ?? []).includes(providerId);
}

function toggleProvider(providerId: string, enabled: boolean): void {
  const current = new Set(settingsForm.enabledResourceProviders ?? []);
  if (enabled) {
    current.add(providerId);
  } else {
    current.delete(providerId);
  }
  // 按当前顺序写回，免得开关一次就把顺序打乱
  settingsForm.enabledResourceProviders = orderedProviders.value
    .map((provider) => provider.id)
    .filter((id) => current.has(id));
}

function moveProvider(providerId: string, delta: number): void {
  const order = orderedProviders.value.map((provider) => provider.id);
  const index = order.indexOf(providerId);
  const target = index + delta;
  if (index === -1 || target < 0 || target >= order.length) {
    return;
  }
  const [moved] = order.splice(index, 1);
  order.splice(target, 0, moved as string);
  settingsForm.resourceProviderOrder = order;
  settingsForm.enabledResourceProviders = order.filter((id) => isProviderEnabled(id));
}

function providerStatusLabel(providerId: string): string {
  const result = providerResults.value[providerId];
  if (!result) {
    return "未测试";
  }
  return result.ok ? `可达 · ${result.count} 条` : `不可达：${result.error ?? "未知错误"}`;
}

function providerStatusClass(providerId: string): string {
  const result = providerResults.value[providerId];
  if (!result) {
    return "provider-status-idle";
  }
  return result.ok ? "provider-status-ok" : "provider-status-error";
}

async function testProvider(providerId: string): Promise<void> {
  providerTesting.value = providerId;
  try {
    // 用一个大概率有结果的关键词，测的是「连得上且解析得出条目」而不只是 HTTP 200
    const status = await testResourceProvider(providerId, "anime");
    providerResults.value = {
      ...providerResults.value,
      [providerId]: { ok: status.ok, count: status.count, error: status.error }
    };
  } catch (error) {
    providerResults.value = {
      ...providerResults.value,
      [providerId]: { ok: false, count: 0, error: error instanceof Error ? error.message : String(error) }
    };
  } finally {
    providerTesting.value = null;
  }
}

// —— 信息平台 ——
// 管的是「这部番叫什么、评分多少、简介和封面」，和上面的资源来源（种子在哪儿）是两条独立链路。
const metadataPlatforms = ref<MetadataPlatformInfo[]>([]);
const platformTesting = ref<string | null>(null);
const platformResults = ref<Record<string, { ok: boolean; count: number; error: string | null }>>({});

function isPlatformEnabled(platformId: string): boolean {
  return (settingsForm.enabledMetadataPlatforms ?? []).includes(platformId);
}

function toggleMetadataPlatform(platformId: string, enabled: boolean): void {
  const current = new Set(settingsForm.enabledMetadataPlatforms ?? []);
  if (enabled) {
    current.add(platformId);
  } else {
    current.delete(platformId);
  }
  // 按后端给的固定顺序写回，保证聚合结果的排序稳定
  settingsForm.enabledMetadataPlatforms = metadataPlatforms.value
    .map((platform) => platform.id)
    .filter((id) => current.has(id));
}

function platformStatusLabel(platform: MetadataPlatformInfo): string {
  const result = platformResults.value[platform.id];
  if (result) {
    return result.ok ? `可达 · ${result.count} 条` : `不可达：${result.error || "未知错误"}`;
  }
  if (platform.requiresApiKey && !platform.configured) {
    return "未填写 API key";
  }
  return "未测试";
}

function platformStatusClass(platform: MetadataPlatformInfo): string {
  const result = platformResults.value[platform.id];
  if (!result) {
    return "provider-status-idle";
  }
  return result.ok ? "provider-status-ok" : "provider-status-error";
}

async function testPlatform(platformId: string): Promise<void> {
  platformTesting.value = platformId;
  try {
    // 用一部几乎必然存在的番做探针：测的是「连得上且解析得出条目」，而不只是 HTTP 200
    const status = await testMetadataPlatform(platformId, "葬送のフリーレン");
    platformResults.value = {
      ...platformResults.value,
      [platformId]: { ok: status.ok, count: status.count, error: status.reason || null }
    };
  } catch (error) {
    platformResults.value = {
      ...platformResults.value,
      [platformId]: { ok: false, count: 0, error: error instanceof Error ? error.message : String(error) }
    };
  } finally {
    platformTesting.value = null;
  }
}

async function loadSourceLists(): Promise<void> {
  try {
    resourceProviders.value = await listResourceProviders();
  } catch {
    // 拉不到就让列表空着显示「正在加载」，不弹错误打断用户改别的设置
  }
  try {
    metadataPlatforms.value = await listMetadataPlatforms();
  } catch {
    // 同上：信息平台列表拉不到也只是空着，不打断用户
  }
}

const liveTunnelState = computed(() => status.value?.tunnelState ?? tunnelStatus.value?.state);
const tunnelPublicUrl = computed(() => status.value?.tunnelPublicUrl ?? tunnelStatus.value?.publicUrl ?? "");
const BINARY_SOURCE_LABELS: Record<string, string> = {
  override: "自定义路径",
  bundled: "内置",
  path: "系统 PATH"
};
const tunnelBinaryLabel = computed(() => {
  const snapshot = tunnelStatus.value;
  if (!snapshot?.binaryAvailable) {
    return "未找到";
  }
  return BINARY_SOURCE_LABELS[snapshot.binarySource ?? "path"] ?? "可用";
});

const externalAccessEnabled = computed({
  get: () => !["127.0.0.1", "localhost"].includes(settingsForm.bindHost.trim().toLowerCase()),
  set: (enabled: boolean) => {
    // bindHost 归在延迟提交那组（它同时也是个文本框），但这里是开关，得立刻存
    settingsForm.bindHost = enabled ? "0.0.0.0" : "127.0.0.1";
    void saveSettingsFields(["bindHost"]);
  }
});

function setDownloadClient(client: AppSettings["downloadClient"]): void {
  settingsForm.downloadClient = client;
  settingsForm.qbittorrentEnabled = client === "qbittorrent";
}

// ─────────────────────── 数据与备份 ───────────────────────

const backups = ref<BackupSummary[]>([]);
const dataDir = ref("");
const backupImportInput = ref<HTMLInputElement | null>(null);
const pendingRestore = ref<{ name: string; label: string; file: File | null } | null>(null);

const loadingBackups = computed(() => isBusy("backups:list"));
const creatingBackup = computed(() => isBusy("backups:create"));
const restoringBackup = computed(() => isBusy("backups:restore"));

const BACKUP_REASON_LABELS: Record<string, string> = {
  manual: "手动",
  "pre-restore": "恢复前自动",
  unknown: "未知来源"
};

function backupReasonLabel(reason: string): string {
  if (reason.startsWith("pre-update-")) {
    return `更新前自动 · ${reason.slice("pre-update-".length)}`;
  }
  return BACKUP_REASON_LABELS[reason] ?? reason;
}

function formatBackupTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function backupFileName(path: string): string {
  return path.split(/[\\/]/).pop() ?? path;
}

async function refreshBackups(): Promise<void> {
  await withBusy("backups:list", async () => {
    try {
      const result = await listBackups();
      backups.value = result.backups;
      dataDir.value = result.dataDir;
    } catch (error) {
      toastError(error, "读取备份列表失败。");
    }
  });
}

async function runOpenDataDir(): Promise<void> {
  try {
    const result = await openDataDir();
    dataDir.value = result.path;
    updateFeedback(result.opened ? "已打开数据目录。" : `数据目录：${result.path}`, result.opened ? "success" : "info");
  } catch (error) {
    toastError(error, "打开数据目录失败。");
  }
}

async function runCreateBackup(): Promise<void> {
  await withBusy("backups:create", async () => {
    try {
      const result = await createBackup("manual");
      backups.value = result.backups;
      updateFeedback(`已备份到 ${result.name}（${formatBytes(result.size)}）。`, "success");
    } catch (error) {
      toastError(error, "创建备份失败。");
    }
  });
}

async function runDownloadBackup(name: string): Promise<void> {
  await withBusy(`backups:download:${name}`, async () => {
    try {
      await downloadBackup(name);
    } catch (error) {
      toastError(error, "下载备份失败。");
    }
  });
}

async function runDeleteBackup(name: string): Promise<void> {
  await withBusy(`backups:delete:${name}`, async () => {
    try {
      backups.value = (await deleteBackup(name)).backups;
      updateFeedback("已删除该备份。", "success");
    } catch (error) {
      toastError(error, "删除备份失败。");
    }
  });
}

// 恢复会整体替换数据库，必须二次确认——弹层里把代价说清楚。
function askRestore(backup: BackupSummary): void {
  pendingRestore.value = {
    name: backup.name,
    label: `${formatBackupTime(backup.createdAt)} · ${backup.name}`,
    file: null
  };
}

function pickBackupFile(event: Event): void {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (file) {
    pendingRestore.value = { name: file.name, label: `导入文件 ${file.name}`, file };
  }
  input.value = "";
}

async function confirmRestore(): Promise<void> {
  const pending = pendingRestore.value;
  if (!pending) {
    return;
  }
  await withBusy("backups:restore", async () => {
    try {
      const result = pending.file ? await importBackup(pending.file) : await restoreBackup(pending.name);
      pendingRestore.value = null;
      await refreshBackups();
      updateFeedback(
        `已从 ${result.restoredFrom} 恢复，恢复前的数据已另存为 ${backupFileName(result.preRestoreBackupPath)}。` +
          "请刷新页面；如果备份里的 token 与现在不同，还需要重新登录。",
        "success"
      );
    } catch (error) {
      toastError(error, "恢复备份失败。");
    }
  });
}

const clearingPlaybackCache = computed(() => isBusy("playback-cache:clear"));

async function runClearPlaybackCache(): Promise<void> {
  await withBusy("playback-cache:clear", async () => {
    try {
      const result = await clearPlaybackCache();
      updateFeedback(
        result.removedCount
          ? `已清理 ${result.removedCount} 份播放缓存，释放 ${formatBytes(result.freedBytes)}。`
          : "没有需要清理的播放缓存。",
        "success"
      );
    } catch (error) {
      toastError(error, "清理播放缓存失败。");
    }
  });
}
</script>

<template>
  <div ref="settingsLayoutRef" class="settings-app">
    <!-- 主导航始终保留；设置分类在内容区内并排显示，手机端换行排列。 -->
    <aside class="settings-sidebar">
      <label class="settings-sidebar-search">
        <AppNavIcon name="search" aria-hidden="true" />
        <input v-model="settingsQuery" type="search" placeholder="搜索设置…" aria-label="搜索设置" />
      </label>

      <nav class="settings-nav" aria-label="设置分类">
        <div v-for="group in PAGE_GROUPS" :key="group.label" class="settings-nav-group">
          <span class="settings-nav-group-label">{{ group.label }}</span>
          <button
            v-for="page in group.pages"
            :key="page.key"
            type="button"
            class="settings-nav-link"
            :class="{
              'settings-nav-link-active': !searching && activePage === page.key,
              'settings-nav-link-dim': searching && !pageMatchCount[page.key]
            }"
            :aria-current="!searching && activePage === page.key ? 'page' : undefined"
            @click="openPage(page.key)"
          >
            <AppNavIcon :name="page.icon" aria-hidden="true" />
            <span class="min-w-0 flex-1 truncate">{{ page.label }}</span>
            <span v-if="searching && pageMatchCount[page.key]" class="settings-nav-count">{{ pageMatchCount[page.key] }}</span>
          </button>
        </div>
      </nav>
    </aside>

    <section ref="settingsShellRef" class="settings-shell">
      <div class="settings-content">
        <header class="settings-page-head">
          <div class="min-w-0">
            <h2 class="settings-page-title">{{ searching ? `搜索「${settingsQuery.trim()}」` : activePageMeta.label }}</h2>
            <p class="settings-page-desc">
              {{ searching ? (visibleSectionIds?.size ? "下面是所有分类里匹配的设置项。" : "没有匹配的设置项，换个关键词试试。") : activePageMeta.desc }}
            </p>
          </div>
          <!--
            自动保存的代价是「存没存上」变得不可见，所以这条指示器是必需品：
            失败时不能只弹个 toast 就完事，得留在原地、能点重试。
          -->
          <span
            v-if="settingsSaveState !== 'idle'"
            class="status-pill shrink-0"
            :class="settingsSaveState === 'error' ? 'status-pill-err' : settingsSaveState === 'saved' ? 'status-pill-ok' : 'status-pill-muted'"
            role="status"
            aria-live="polite"
            :title="settingsSaveState === 'error' ? settingsSaveError ?? undefined : undefined"
          >
            {{ SAVE_STATE_LABEL[settingsSaveState] }}
            <button v-if="settingsRetryable" type="button" class="ml-1 underline" @click="retrySettingsSave">重试</button>
          </span>
          <span v-else class="shrink-0 pt-1.5 text-xs text-[var(--muted)]">改动自动保存</span>
        </header>

      <form class="settings-panel" :class="{ 'settings-single-section': singleSectionPage }" @submit.prevent="commitTextFields" @focusout="commitTextFields" @keydown.enter="commitTextFields">
        <div v-show="categoryShown('general')" class="settings-category settings-list">
          <h2 v-show="searching" class="settings-category-title">常规</h2>
          <section id="settings-section-appearance" class="settings-section" data-section="appearance" v-show="sectionShown('appearance')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">外观</h3>
            </header>
            <div class="settings-group">
              <!-- 外观：明暗模式 + 主题色。换色立刻生效（只写根元素变量），保存才落库。 -->
              <div class="settings-item settings-item-stacked appearance-item">
                <span>
                  <span class="settings-desc">明暗模式随时生效；主题色只选一个颜色，悬停色、选中底色、深色模式那版都由它算出来。</span>
                </span>

                <div class="appearance-rows">
                  <div class="appearance-row">
                    <span class="appearance-label">明暗模式</span>
                    <span class="appearance-seg" role="radiogroup" aria-label="明暗模式">
                      <button
                        v-for="option in THEME_MODE_OPTIONS"
                        :key="option.key"
                        type="button"
                        role="radio"
                        :aria-checked="themeMode === option.key"
                        :class="['appearance-seg-item', themeMode === option.key ? 'is-on' : '']"
                        @click="setThemeMode(option.key)"
                      >
                        {{ option.label }}
                      </button>
                    </span>
                  </div>

                  <div class="appearance-row">
                    <span class="appearance-label">主题色</span>
                    <div class="appearance-accent">
                      <div class="appearance-presets">
                        <button
                          v-for="preset in ACCENT_PRESETS"
                          :key="preset.value"
                          type="button"
                          :class="['appearance-chip', settingsForm.themeAccent === preset.value ? 'is-on' : '']"
                          :style="{ '--chip-color': preset.value }"
                          @click="pickAccent(preset.value)"
                        >
                          <span class="appearance-dot"></span>
                          {{ preset.label }}
                        </button>
                      </div>
                      <div class="appearance-custom">
                        <label class="appearance-chip appearance-chip-custom">
                          <span class="appearance-dot appearance-dot-rainbow"></span>
                          自定义
                          <input
                            type="color"
                            class="appearance-color-input"
                            :value="settingsForm.themeAccent"
                            aria-label="自定义主题色"
                            @input="pickAccent(($event.target as HTMLInputElement).value, false)"
                            @change="pickAccent(($event.target as HTMLInputElement).value)"
                          />
                        </label>
                        <input
                          v-model="accentHexDraft"
                          class="settings-input appearance-hex"
                          spellcheck="false"
                          aria-label="主题色 hex"
                          placeholder="#0041FD"
                          @blur="commitAccentHex"
                          @keydown.enter.prevent="commitAccentHex"
                        />
                        <button type="button" class="appearance-reset" @click="pickAccent(DEFAULT_THEME_ACCENT)">恢复默认</button>
                      </div>
                    </div>
                  </div>

                  <div class="appearance-row appearance-preview-row">
                    <span class="appearance-label">预览</span>
                    <div class="appearance-preview">
                      <span class="appearance-preview-primary">主操作</span>
                      <span class="appearance-preview-ghost">次操作</span>
                      <span class="appearance-preview-selected">选中项</span>
                      <span class="appearance-preview-bar"><span :style="{ width: '62%' }"></span></span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <section id="settings-section-run" class="settings-section" data-section="run" v-show="sectionShown('run')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">运行</h3>
            </header>
            <div class="settings-group">
              <label class="settings-item">
                <span>
                  <span class="settings-title">轮询间隔</span>
                  <span class="settings-desc">多久检查一次订阅更新与下载状态，单位分钟</span>
                </span>
                <span class="settings-combo settings-combo-stacked">
                  <input v-model.number="settingsForm.pollIntervalMinutes" type="number" class="settings-input settings-input-short" :class="{ 'settings-input-invalid': settingsFieldErrors.pollIntervalMinutes }" />
                  <span v-if="settingsFieldErrors.pollIntervalMinutes" class="settings-field-error">{{ settingsFieldErrors.pollIntervalMinutes }}</span>
                </span>
              </label>
            </div>
          </section>

          <section class="settings-section"><header class="settings-section-head"><h3 class="settings-section-title">Go 服务</h3></header><p class="settings-desc">关闭浏览器不会停止下载。服务随 Go 进程运行；开机自启可通过 Docker restart 策略或系统服务配置。</p></section>

          <section id="settings-section-data" class="settings-section" data-section="data" v-show="sectionShown('data')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">备份与恢复</h3>
            </header>
            <div class="settings-group">
              <div class="settings-item settings-item-stacked">
                <span>
                  <span class="settings-title">数据目录与备份</span>
                  <span class="settings-desc">
                    订阅、下载记录和设置都在数据目录的 app.db 里。备份是一份一致性快照，可下载留存，也可随时恢复。
                  </span>
                </span>
                <div class="grid w-full min-w-0 gap-3">
                  <div class="flex flex-wrap items-center gap-3">
                    <code class="min-w-0 flex-1 break-all rounded-lg border border-[var(--line)] px-3 py-2 text-xs text-[var(--muted)]">
                      {{ dataDir || "读取中…" }}
                    </code>
                    <button type="button" class="settings-secondary-button" @click="runOpenDataDir()">打开目录</button>
                    <button
                      type="button"
                      class="settings-secondary-button"
                      :disabled="creatingBackup"
                      @click="runCreateBackup()"
                    >
                      {{ creatingBackup ? "备份中…" : "立即备份" }}
                    </button>
                    <button
                      type="button"
                      class="settings-secondary-button"
                      :disabled="restoringBackup"
                      @click="backupImportInput?.click()"
                    >
                      导入备份文件
                    </button>
                    <input
                      ref="backupImportInput"
                      type="file"
                      accept=".zip,application/zip"
                      class="hidden"
                      @change="pickBackupFile"
                    />
                  </div>

                  <p v-if="loadingBackups" class="text-sm text-[var(--muted)]">读取中…</p>
                  <p v-else-if="!backups.length" class="text-sm text-[var(--muted)]">还没有备份。</p>
                  <div
                    v-for="backup in backups"
                    v-else
                    :key="backup.name"
                    class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-[var(--line)] px-3 py-2"
                  >
                    <span class="min-w-0 text-sm text-[var(--ink)]">
                      <strong>{{ formatBackupTime(backup.createdAt) }}</strong>
                      <span class="ml-2 text-xs text-[var(--muted)]">{{ formatBytes(backup.size) }}</span>
                      <span class="ml-2 block text-xs text-[var(--muted)]">
                        {{ backupReasonLabel(backup.reason) }} · {{ backup.name }}
                      </span>
                    </span>
                    <span class="flex flex-wrap gap-2">
                      <button type="button" class="settings-secondary-button" @click="runDownloadBackup(backup.name)">
                        下载
                      </button>
                      <button
                        type="button"
                        class="settings-secondary-button"
                        :disabled="restoringBackup"
                        @click="askRestore(backup)"
                      >
                        恢复
                      </button>
                      <button type="button" class="settings-secondary-button" @click="runDeleteBackup(backup.name)">
                        删除
                      </button>
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </section>
        </div>

        <div v-show="categoryShown('download')" class="settings-category settings-list">
          <h2 v-show="searching" class="settings-category-title">下载</h2>
          <section id="settings-section-download" class="settings-section" data-section="download" v-show="sectionShown('download')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">目录与下载器</h3>
            </header>
            <div class="settings-group">
              <label class="settings-item settings-item-stacked">
                <span>
                  <span class="settings-title">下载目录</span>
                  <span class="settings-desc">新任务默认存到这里</span>
                </span>
                <input v-model="settingsForm.downloadDir" class="settings-input settings-input-wide" />
              </label>

              <CloudSetup ref="cloudSetup" @connected="refreshCloud" />
              <label class="settings-item">
                <span><span class="settings-title">自动归档到 Google Drive</span><span class="settings-desc">完整下载后上传全部选中文件；失败保留本地文件，每 5 分钟重试</span></span>
                <input v-model="settingsForm.cloudArchiveEnabled" type="checkbox" class="settings-switch" />
              </label>
              <div class="settings-item">
                <span><span class="settings-title">Google Drive 授权</span><span class="settings-desc">{{ cloudConnected ? '已连接，凭据仅保存在服务端' : '尚未连接，请使用上方配置向导' }}</span></span>
                <button class="settings-secondary-button" @click="cloudConnected ? authorizeCloud() : cloudSetup?.start()">{{ cloudConnected ? '断开连接' : '连接 Google Drive' }}</button>
              </div>
              <details class="settings-item-stacked p-4">
                <summary class="settings-title cursor-pointer">高级设置：网络直连、TLS 与手动配置</summary>
              <div class="settings-item settings-item-stacked">
                <label class="flex items-center justify-between gap-4">
                  <span><span class="settings-title">Google Drive IP 直连</span><span class="settings-desc">开启后，上传及云端播放使用最近 24 小时内探测最快的可用 IP；无可用地址时走系统网络。OAuth 授权仍走系统网络。</span></span>
                  <input type="checkbox" class="settings-switch" :checked="driveNetwork?.enabled ?? false" :disabled="!driveNetwork || driveNetworkSaving" @change="toggleDriveNetwork" />
                </label>
                <label class="settings-title" for="drive-sni-mode">TLS SNI 模式</label>
                <select id="drive-sni-mode" v-model="driveSNIMode" class="settings-input settings-input-wide" :disabled="driveScanning || driveNetworkSaving">
                  <option value="original">原始 Google 域名（默认）</option>
                  <option value="none">不发送 SNI</option>
                  <option value="custom">自定义 SNI 域名</option>
                </select>
                <input v-if="driveSNIMode === 'custom'" v-model="driveSNIHost" aria-label="自定义 SNI 域名" class="settings-input settings-input-wide" placeholder="仅域名，不含 https://、端口或路径" :disabled="driveScanning || driveNetworkSaving" />
                <div class="flex flex-wrap items-center gap-3">
                  <button class="settings-secondary-button" :disabled="!driveNetwork || driveScanning || driveNetworkSaving" @click="saveDriveTLS">保存 SNI 设置</button>
                  <span class="settings-desc">修改模式会清除旧扫描结果；扫描按钮会先保存当前模式。</span>
                </div>
                <p class="settings-desc">仅修改握手发送的 SNI，证书链、有效期和真实 Google 域名仍会校验。扫描、上传及云端播放使用相同模式；OAuth 使用系统网络。不发送或自定义 SNI 不保证所有网络都可直连。</p>
                <label class="settings-title" for="drive-network-candidates">自定义候选 IP（可选）</label>
                <textarea id="drive-network-candidates" v-model="driveCandidates" class="settings-input settings-input-wide" rows="3" placeholder="每行一个公网 IP，支持 # 注释。留空使用 DNS、历史记录和内置候选。最多 256 个。" />
                <div class="flex flex-wrap items-center gap-3 break-all">
                  <button class="settings-secondary-button" :disabled="!driveNetwork || driveScanning || driveNetworkSaving" @click="scanCloudNetwork">{{ driveScanning ? '正在探测，最长约 60 秒…' : '扫描可用 IP' }}</button>
                  <span class="settings-desc">{{ driveNetwork?.enabled ? (driveNetwork.selectedIP ? `当前选择：${driveNetwork.selectedIP}` : '当前使用系统网络：没有有效候选') : '直连未启用，当前使用系统网络' }}</span>
                </div>
                <p class="settings-desc">扫描在运行 bgmcat 的机器上进行。这里测量响应时间，不代表实际下载带宽；不会生成浏览器直链。TUN 代理仍可能接管网络。</p>
                <p v-if="driveNetwork?.scannedAt" class="settings-desc">最近扫描：{{ new Date(driveNetwork.scannedAt).toLocaleString() }}{{ driveNetwork.partial ? '（达到时间上限，部分候选未完成）' : '' }}</p>
                <div v-if="driveNetwork?.results.length" class="max-h-56 overflow-auto">
                  <table class="w-full text-left text-xs">
                    <thead><tr><th class="p-2">IP</th><th class="p-2">探测耗时</th><th class="p-2">状态</th></tr></thead>
                    <tbody><tr v-for="result in driveNetwork.results" :key="result.ip">
                      <td class="p-2 font-mono">{{ result.ip }}</td><td class="p-2">{{ result.available ? `${result.latencyMs} ms` : '—' }}</td>
                      <td class="p-2">{{ result.available ? (Date.now() / 1000 - result.verifiedAt >= 86400 ? '已过期，请重新扫描' : result.warning || 'API 与下载探测通过') : result.error }}</td>
                    </tr></tbody>
                  </table>
                </div>
              </div>
              <template v-if="true">
                <label class="settings-item settings-item-stacked">
                  <span class="settings-title">Google OAuth 客户端 ID</span>
                  <input v-model="settingsForm.googleClientId" class="settings-input settings-input-wide" placeholder="例如 xxx.apps.googleusercontent.com" />
                </label>
                <label class="settings-item settings-item-stacked">
                  <span><span class="settings-title">Google OAuth 客户端密钥</span><span class="settings-desc">在 Google Cloud 创建 Web 应用 OAuth 客户端，并启用 Drive API</span></span>
                  <input v-model="settingsForm.googleClientSecret" class="settings-input settings-input-wide" type="password" autocomplete="off" placeholder="OAuth Client Secret" />
                </label>
                <label class="settings-item settings-item-stacked">
                  <span><span class="settings-title">Google Drive 目标文件夹 ID</span><span class="settings-desc">填写文件夹链接中 folders/ 后的 ID，root 表示云盘根目录</span></span>
                  <input v-model="settingsForm.cloudDriveFolderId" class="settings-input settings-input-wide" placeholder="root" />
                </label>
                <label class="settings-item settings-item-stacked">
                  <span><span class="settings-title">OAuth 回调地址</span><span class="settings-desc">将这个地址添加到 Google OAuth 客户端的授权重定向 URI；播放由 Go 按需读取云端数据</span></span>
                  <input v-model="settingsForm.googleRedirectUri" class="settings-input settings-input-wide" placeholder="http://127.0.0.1:3001/api/cloud/callback" />
                </label>
                <label class="settings-item">
                  <span><span class="settings-title">归档后清理本地文件</span><span class="settings-desc">仅在云端大小与 MD5 校验通过、云端可读取且文件没有正在播放后删除；未完成配置时保留本地文件</span></span>
                  <input v-model="settingsForm.cloudDeleteLocal" type="checkbox" class="settings-switch" />
                </label>
              </template>
              </details>
              <div class="settings-item">
                <span>
                  <span class="settings-title">下载器</span>
                  <span class="settings-desc">Go 托管 aria2 HTTP / BitTorrent 下载器，也可连接 qBittorrent</span>
                </span>
                <div class="settings-segmented" role="group" aria-label="下载器">
                  <button
                    type="button"
                    class="settings-segmented-button"
                    :class="{ 'settings-segmented-button-active': settingsForm.downloadClient === 'native' }"
                    @click="setDownloadClient('native')"
                  >
                    托管 aria2
                  </button>
                  <button
                    type="button"
                    class="settings-segmented-button"
                    :class="{ 'settings-segmented-button-active': settingsForm.downloadClient === 'qbittorrent' }"
                    @click="setDownloadClient('qbittorrent')"
                  >
                    qBittorrent
                  </button>
                </div>
              </div>
            </div>
          </section>

          <section id="settings-section-qbittorrent" class="settings-section" data-section="qbittorrent" v-show="sectionShown('qbittorrent')" v-if="settingsForm.downloadClient === 'qbittorrent'">
            <header class="settings-section-head">
              <h3 class="settings-section-title">qBittorrent</h3>
            </header>
            <div class="settings-group">
              <!-- 选下载器时会一并打开它；只有旧配置里「选了 qBittorrent 却关着连接」才需要这一行 -->
              <label v-if="!settingsForm.qbittorrentEnabled" class="settings-item">
                <span>
                  <span class="settings-title">连接已停用</span>
                  <span class="settings-desc">当前不会向 qBittorrent 发送任何任务，打开后才会生效</span>
                </span>
                <input v-model="settingsForm.qbittorrentEnabled" type="checkbox" class="settings-switch" />
              </label>

              <label class="settings-item settings-item-stacked">
                <span>
                  <span class="settings-title">Web UI 地址</span>
                  <span class="settings-desc">qBittorrent 设置里「Web UI」那一页的地址，例如 http://127.0.0.1:8080</span>
                </span>
                <input v-model="settingsForm.qbittorrentBaseUrl" class="settings-input settings-input-wide" placeholder="http://127.0.0.1:8080" />
              </label>

              <label class="settings-item">
                <span>
                  <span class="settings-title">登录账号</span>
                  <span class="settings-desc">Web UI 的用户名与密码</span>
                </span>
                <span class="settings-pair">
                  <input v-model="settingsForm.qbittorrentUsername" class="settings-input" autocomplete="off" placeholder="用户名" aria-label="qBittorrent 用户名" />
                  <input v-model="settingsForm.qbittorrentPassword" type="text" class="secret-mask-input settings-input" autocomplete="off" placeholder="密码" aria-label="qBittorrent 密码" />
                </span>
              </label>
            </div>
          </section>

          <section id="settings-section-playback" class="settings-section" data-section="playback" v-show="sectionShown('playback')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">在线播放</h3>
            </header>
            <div class="settings-group">
              <div class="settings-item">
                <span>
                  <span class="settings-title">播放缓存</span>
                  <span class="settings-desc">在线播放 MKV / HEVC 时产生的转码分片；清理会中断正在进行的转码播放</span>
                </span>
                <button
                  type="button"
                  class="btn-ghost h-9 rounded-md px-4 text-sm font-semibold"
                  :disabled="clearingPlaybackCache"
                  @click="runClearPlaybackCache"
                >
                  {{ clearingPlaybackCache ? "清理中…" : "清理播放缓存" }}
                </button>
              </div>
            </div>
            <details class="settings-advanced">
              <summary>高级</summary>
              <div class="settings-group">
                <label class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">ffmpeg 路径</span>
                    <span class="settings-desc">留空使用随应用打包的 ffmpeg；填写可执行文件路径可覆盖，ffprobe 取同目录的同名文件</span>
                  </span>
                  <input
                    v-model="settingsForm.ffmpegPath"
                    class="settings-input settings-input-wide"
                    placeholder="留空使用内置 ffmpeg，例如 /opt/homebrew/bin/ffmpeg"
                  />
                </label>
              </div>
            </details>
          </section>
        </div>

        <div v-show="categoryShown('sources')" class="settings-category settings-list">
          <h2 v-show="searching" class="settings-category-title">资源来源</h2>
          <section id="settings-section-sites" class="settings-section" data-section="sites" v-show="sectionShown('sites')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">资源站</h3>
              <p class="settings-section-desc">种子从哪儿找。详情页和订阅都会跨这些站合并，越靠上越优先。</p>
            </header>
            <div class="settings-group">
              <!--
                番剧资源的搜索与下载可以同时用多个站点。这里列出全部来源：
                开关决定详情页查不查它、订阅时挂不挂它；顺序决定跨源合并时用谁的标题/链接展示，
                也是同集择优里「来源」这一档的先后。
              -->
              <div class="settings-item settings-item-stacked">
                <div class="provider-list">
                  <div v-for="(provider, index) in orderedProviders" :key="provider.id" class="provider-row">
                    <input
                      :checked="isProviderEnabled(provider.id)"
                      type="checkbox"
                      class="settings-switch"
                      :aria-label="`启用 ${provider.displayName}`"
                      @change="toggleProvider(provider.id, ($event.target as HTMLInputElement).checked)"
                    />
                    <span class="provider-name">{{ provider.displayName }}</span>
                    <span class="provider-status" :class="providerStatusClass(provider.id)">
                      {{ providerStatusLabel(provider.id) }}
                    </span>
                    <button
                      type="button"
                      class="btn-ghost provider-button"
                      :disabled="providerTesting === provider.id"
                      @click="testProvider(provider.id)"
                    >
                      {{ providerTesting === provider.id ? "测试中…" : "测试" }}
                    </button>
                    <button
                      type="button"
                      class="btn-ghost provider-button"
                      :disabled="index === 0"
                      aria-label="上移"
                      @click="moveProvider(provider.id, -1)"
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      class="btn-ghost provider-button"
                      :disabled="index === orderedProviders.length - 1"
                      aria-label="下移"
                      @click="moveProvider(provider.id, 1)"
                    >
                      ↓
                    </button>
                  </div>
                  <p v-if="!orderedProviders.length" class="settings-desc">正在加载资源来源…</p>
                </div>
              </div>
            </div>
          </section>

          <section id="settings-section-mikan" class="settings-section" data-section="mikan" v-show="sectionShown('mikan')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">蜜柑计划</h3>
            </header>
            <div class="settings-group">
              <label class="settings-item settings-item-stacked">
                <span>
                  <span class="settings-title">蜜柑计划地址</span>
                  <span class="settings-desc">大陆网络建议使用 mikanime.tv；mikanani.me 可能超时</span>
                </span>
                <input v-model="settingsForm.mikanBaseUrl" class="settings-input settings-input-wide" placeholder="https://mikanime.tv 或 https://mikanani.me" />
              </label>


            </div>
          </section>

          <section id="settings-section-metadata" class="settings-section" data-section="metadata" v-show="sectionShown('metadata')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">番剧信息</h3>
              <p class="settings-section-desc">番名、评分、简介和封面从哪儿来；各平台评分分别列出，不做平均。</p>
            </header>
            <div class="settings-group">
              <!--
                信息平台管的是番剧本身的信息（标题/评分/简介/封面），不是种子。
                多开几个平台能搜到本地库里没有的番；某个平台不通时结果照出，只少它那一份。
              -->
              <div class="settings-item settings-item-stacked">
                <div class="provider-list">
                  <div v-for="platform in metadataPlatforms" :key="platform.id" class="metadata-platform-row">
                    <input
                      :checked="isPlatformEnabled(platform.id)"
                      type="checkbox"
                      class="settings-switch"
                      :aria-label="`启用 ${platform.displayName}`"
                      @change="toggleMetadataPlatform(platform.id, ($event.target as HTMLInputElement).checked)"
                    />
                    <span class="provider-name">{{ platform.displayName }}</span>
                    <span class="provider-status" :class="platformStatusClass(platform)">
                      {{ platformStatusLabel(platform) }}
                    </span>
                    <button
                      type="button"
                      class="btn-ghost provider-button"
                      :disabled="platformTesting === platform.id"
                      @click="testPlatform(platform.id)"
                    >
                      {{ platformTesting === platform.id ? "测试中…" : "测试" }}
                    </button>
                  </div>
                  <p v-if="!metadataPlatforms.length" class="settings-desc">正在加载信息平台…</p>
                </div>
              </div>

              <label class="settings-item settings-item-stacked">
                <span>
                  <span class="settings-title">TMDB API key</span>
                  <span class="settings-desc">
                    留空就不请求 TMDB。填好后点上面 TMDB 那行的「测试」确认。
                    本产品使用 TMDB API，但未经 TMDB 认可或认证。
                  </span>
                </span>
                <input
                  v-model="settingsForm.tmdbApiKey"
                  class="settings-input settings-input-wide"
                  type="password"
                  autocomplete="off"
                  placeholder="留空则不启用 TMDB"
                />
              </label>
            </div>
          </section>
        </div>

        <div v-show="categoryShown('remote')" class="settings-category settings-list">
          <h2 v-show="searching" class="settings-category-title">远程访问</h2>
          <section id="settings-section-remote-status" class="settings-section" data-section="remote-status" v-show="sectionShown('remote-status')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">访问状态</h3>
            </header>
          <div class="settings-summary-grid">
            <button type="button" class="settings-summary-item text-left" @click="copyText(status?.apiBaseUrl ?? '')">
              <span class="settings-summary-label">本机地址（点击复制）</span>
              <strong class="break-all">{{ status?.apiBaseUrl ?? "-" }}</strong>
            </button>
            <div class="settings-summary-item">
              <span class="settings-summary-label">本机鉴权</span>
              <strong>{{ status?.localAuthBypassEnabled ? "本机免 token" : "需要 token" }}</strong>
            </div>
            <div class="settings-summary-item">
              <span class="settings-summary-label">隧道</span>
              <strong>
                {{ tunnelStateLabel(liveTunnelState) }}
                <template v-if="tunnelStatus?.readyConnections"> · {{ tunnelStatus.readyConnections }} 条连接</template>
              </strong>
            </div>
            <button
              type="button"
              class="settings-summary-item text-left"
              :disabled="!tunnelPublicUrl"
              @click="copyText(tunnelPublicUrl)"
            >
              <span class="settings-summary-label">公网地址（点击复制）</span>
              <strong class="break-all">{{ tunnelPublicUrl || "-" }}</strong>
            </button>
          </div>
          </section>

          <section id="settings-section-token" class="settings-section" data-section="token" v-show="sectionShown('token')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">访问口令</h3>
            </header>
            <div class="settings-group">
              <label class="settings-item settings-item-stacked">
                <span>
                  <span class="settings-title">访问口令（Token）</span>
                  <span class="settings-desc">从其他设备打开 WebUI 时要填的口令</span>
                </span>
                <div class="flex w-full min-w-0 flex-col gap-3 sm:flex-row">
                  <input
                    v-model="settingsForm.token"
                    type="text"
                    class="secret-mask-input settings-input min-w-0 flex-1"
                    autocomplete="off"
                    autocapitalize="off"
                    autocorrect="off"
                    spellcheck="false"
                    inputmode="text"
                  />
                  <button
                    type="button"
                    class="settings-secondary-button"
                    :disabled="!settingsForm.token.trim()"
                    @click="copyText(settingsForm.token)"
                  >
                    复制 Token
                  </button>
                  <button
                    type="button"
                    class="settings-secondary-button"
                    :disabled="rotatingToken"
                    @click="rotateToken()"
                  >
                    重新生成
                  </button>
                </div>
              </label>
            </div>
          </section>

          <section id="settings-section-lan" class="settings-section" data-section="lan" v-show="sectionShown('lan')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">局域网访问</h3>
            </header>
            <div class="settings-group">
              <label class="settings-item">
                <span>
                  <span class="settings-title">允许局域网 / 外网访问</span>
                  <span class="settings-desc">监听地址由启动参数控制。例如 --listen 0.0.0.0:3001 允许局域网访问。</span>
                </span>
                <input v-model="externalAccessEnabled" :disabled="isSettingLocked('bindHost')" type="checkbox" class="settings-switch" />
              </label>
            </div>
            <details class="settings-advanced">
              <summary>高级</summary>
              <div class="settings-group">
                <label class="settings-item">
                  <span>
                    <span class="settings-title">监听地址与端口</span>
                    <span class="settings-desc">WebUI/API 的 Bind Host 和 Port</span>
                  </span>
                  <div class="settings-combo settings-combo-stacked">
                    <span class="settings-combo-row">
                      <input v-model="settingsForm.bindHost" class="settings-input" :class="{ 'settings-input-invalid': settingsFieldErrors.bindHost }" aria-label="Bind Host" :disabled="isSettingLocked('bindHost')" />
                      <input v-model.number="settingsForm.port" type="number" class="settings-input settings-input-short" :class="{ 'settings-input-invalid': settingsFieldErrors.port }" aria-label="Port" :disabled="isSettingLocked('port')" />
                    </span>
                    <!-- 容器部署里这两项由环境变量定死。不说明的话,用户改完保存、页面回弹,会以为是 bug -->
                    <span v-if="isSettingLocked('bindHost') || isSettingLocked('port')" class="settings-desc">
                      由环境变量接管（ANIME_RSS_BIND_HOST / ANIME_RSS_PORT），要改请改部署配置后重启容器。
                    </span>
                    <span v-if="settingsFieldErrors.bindHost || settingsFieldErrors.port" class="settings-field-error">
                      {{ settingsFieldErrors.bindHost ?? settingsFieldErrors.port }}
                    </span>
                  </div>
                </label>

                <label class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">允许的跨域来源（CORS）</span>
                    <span class="settings-desc">多个来源用英文逗号分隔；开隧道时上面的检查会提示要不要加</span>
                  </span>
                  <input
                    :value="settingsForm.corsOrigins.join(', ')"
                    class="settings-input settings-input-wide"
                    @input="settingsForm.corsOrigins = ($event.target as HTMLInputElement).value.split(',').map((item) => item.trim()).filter(Boolean)"
                  />
                </label>
              </div>
            </details>
          </section>

          <section id="settings-section-tunnel" class="settings-section" data-section="tunnel" v-show="sectionShown('tunnel')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">Cloudflare 隧道</h3>
              <p class="settings-section-desc">不开端口、不配路由器，把 WebUI 安全地放到公网上。</p>
            </header>
            <div class="tunnel-checks">
              <p
                v-for="issue in [...tunnelBlockers, ...tunnelWarnings]"
                :key="issue.code"
                class="tunnel-check"
                :class="issue.level === 'block' ? 'tunnel-check-block' : 'tunnel-check-warn'"
              >
                <strong>{{ issue.level === "block" ? "阻断" : "提醒" }}</strong>
                <span>{{ issue.message }}</span>
                <button
                  v-if="issue.code === 'corsRestricted'"
                  type="button"
                  class="tunnel-check-fix"
                  @click="addTunnelOriginToCors()"
                >
                  加入允许来源
                </button>
              </p>
              <p v-if="!tunnelBlockers.length" class="tunnel-check tunnel-check-pass">
                <strong>已就绪</strong>
                <span>隧道开启后本机不再免 token，公网与本机入口统一要求鉴权。</span>
              </p>
            </div>
            <div class="settings-group">
              <label class="settings-item">
                <span>
                  <span class="settings-title">启用隧道</span>
                  <span class="settings-desc">开启后由应用托管 cloudflared，把 WebUI 暴露到公网，并随应用一起启动。</span>
                </span>
                <input
                  :checked="settingsForm.tunnelEnabled"
                  :disabled="tunnelBusy || (!settingsForm.tunnelEnabled && tunnelBlockers.length > 0)"
                  type="checkbox"
                  class="settings-switch"
                  @change="setTunnelEnabled(($event.target as HTMLInputElement).checked)"
                />
              </label>

              <div class="settings-item">
                <span>
                  <span class="settings-title">隧道模式</span>
                  <span class="settings-desc">临时隧道即开即用但地址随机；命名隧道使用你自己的域名。</span>
                </span>
                <div class="settings-segmented" role="group" aria-label="隧道模式">
                  <button
                    type="button"
                    class="settings-segmented-button"
                    :class="{ 'settings-segmented-button-active': settingsForm.tunnelMode === 'quick' }"
                    @click="settingsForm.tunnelMode = 'quick'"
                  >
                    临时隧道
                  </button>
                  <button
                    type="button"
                    class="settings-segmented-button"
                    :class="{ 'settings-segmented-button-active': settingsForm.tunnelMode === 'named' }"
                    @click="settingsForm.tunnelMode = 'named'"
                  >
                    命名隧道
                  </button>
                </div>
              </div>

              <template v-if="settingsForm.tunnelMode === 'named'">
                <label class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">公网主机名</span>
                    <span class="settings-desc">例如 anime.example.com，需要已在 Cloudflare 完成 DNS 路由。</span>
                  </span>
                  <input v-model="settingsForm.tunnelHostname" class="settings-input settings-input-wide" placeholder="anime.example.com" />
                </label>

                <div class="settings-item">
                  <span>
                    <span class="settings-title">凭据方式</span>
                    <span class="settings-desc">凭据文件按 UUID 运行，无需 cert.pem；token 由 Cloudflare 面板生成。</span>
                  </span>
                  <div class="settings-segmented" role="group" aria-label="凭据方式">
                    <button
                      type="button"
                      class="settings-segmented-button"
                      :class="{ 'settings-segmented-button-active': settingsForm.tunnelCredentialMode === 'credentials-file' }"
                      @click="settingsForm.tunnelCredentialMode = 'credentials-file'"
                    >
                      凭据文件
                    </button>
                    <button
                      type="button"
                      class="settings-segmented-button"
                      :class="{ 'settings-segmented-button-active': settingsForm.tunnelCredentialMode === 'token' }"
                      @click="settingsForm.tunnelCredentialMode = 'token'"
                    >
                      Token
                    </button>
                  </div>
                </div>

                <template v-if="settingsForm.tunnelCredentialMode === 'credentials-file'">
                  <label class="settings-item settings-item-stacked">
                    <span>
                      <span class="settings-title">隧道 UUID</span>
                      <span class="settings-desc">cloudflared tunnel create 生成的隧道 ID</span>
                    </span>
                    <input v-model="settingsForm.tunnelId" class="settings-input settings-input-wide" />
                  </label>

                  <label class="settings-item settings-item-stacked">
                    <span>
                      <span class="settings-title">凭据文件路径</span>
                      <span class="settings-desc">通常在 ~/.cloudflared/&lt;uuid&gt;.json</span>
                    </span>
                    <input v-model="settingsForm.tunnelCredentialsFile" class="settings-input settings-input-wide" />
                  </label>

                  <div v-if="discoveredTunnels.length" class="settings-item settings-item-stacked">
                    <span>
                      <span class="settings-title">本机已有隧道</span>
                      <span class="settings-desc">从 ~/.cloudflared 读取，点击即可填入上面的字段。</span>
                    </span>
                    <div class="tunnel-discovered">
                      <button
                        v-for="item in discoveredTunnels"
                        :key="item.uuid"
                        type="button"
                        class="settings-secondary-button tunnel-discovered-item"
                        @click="applyDiscoveredTunnel(item)"
                      >
                        {{ item.hostname ?? item.uuid }}
                      </button>
                    </div>
                  </div>
                </template>

                <label v-else class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">隧道 Token</span>
                    <span class="settings-desc">仅保存在本机，启动时写入 0600 权限的文件，不会出现在进程参数里。</span>
                  </span>
                  <input
                    v-model="settingsForm.tunnelToken"
                    type="text"
                    class="secret-mask-input settings-input settings-input-wide"
                    autocomplete="off"
                    spellcheck="false"
                  />
                </label>
              </template>

              <label class="settings-item">
                <span>
                  <span class="settings-title">崩溃自动重启</span>
                  <span class="settings-desc">cloudflared 异常退出后按 1s→30s 退避重试，最多 8 次。</span>
                </span>
                <input v-model="settingsForm.tunnelAutoRestart" type="checkbox" class="settings-switch" />
              </label>

              <div class="settings-item">
                <span>
                  <span class="settings-title">重启隧道</span>
                  <span class="settings-desc">改完主机名或端口后用它让新配置生效。</span>
                </span>
                <button
                  type="button"
                  class="settings-secondary-button"
                  :disabled="tunnelBusy || !settingsForm.tunnelEnabled"
                  @click="restartTunnelAction()"
                >
                  {{ tunnelBusy ? "处理中…" : "重启" }}
                </button>
              </div>
            </div>
            <details class="settings-advanced">
              <summary>高级</summary>
              <div class="settings-group">
                <label class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">cloudflared 路径</span>
                    <span class="settings-desc">留空时优先用内置版本，其次是系统 PATH。当前：{{ tunnelBinaryLabel }}</span>
                  </span>
                  <input v-model="settingsForm.tunnelBinaryPath" class="settings-input settings-input-wide" placeholder="自动" />
                </label>
              </div>
            </details>
            <details class="tunnel-logs">
              <summary @click="refreshTunnelLogs()">隧道日志（最近 {{ tunnelLogs.length }} 条）</summary>
              <ol class="tunnel-log-list">
                <li v-for="entry in tunnelLogs" :key="entry.id" :class="`tunnel-log-${entry.level}`">
                  <code>{{ entry.createdAt.slice(11, 19) }}</code>
                  <span>{{ entry.message }}</span>
                </li>
                <li v-if="!tunnelLogs.length">暂无日志。</li>
              </ol>
            </details>
          </section>

          <section id="settings-section-github" class="settings-section" data-section="github" v-show="sectionShown('github')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">GitHub 登录</h3>
            </header>
            <div class="settings-group">
              <label class="settings-item">
                <span>
                  <span class="settings-title">允许用 GitHub 账号登录</span>
                  <span class="settings-desc">从公网打开时可以用 GitHub 登录，不必再粘 token。开启后填写下面几项。</span>
                </span>
                <input v-model="settingsForm.githubOAuthEnabled" type="checkbox" class="settings-switch" />
              </label>

              <template v-if="settingsForm.githubOAuthEnabled">
                <label class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">GitHub Client ID</span>
                    <span class="settings-desc">GitHub OAuth App 的 Client ID</span>
                  </span>
                  <input
                    v-model="settingsForm.githubClientId"
                    class="settings-input settings-input-wide"
                    autocomplete="off"
                    spellcheck="false"
                  />
                </label>

                <label class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">GitHub Client Secret</span>
                    <span class="settings-desc">回调地址填 <code>https://你的域名/api/auth/github/callback</code></span>
                  </span>
                  <input
                    v-model="settingsForm.githubClientSecret"
                    class="secret-mask-input settings-input settings-input-wide"
                    autocomplete="off"
                    spellcheck="false"
                  />
                </label>

                <label class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">允许登录的 GitHub 用户名</span>
                    <span class="settings-desc">多个用英文逗号分隔，忽略大小写；<strong>留空则无人可登录</strong>。</span>
                  </span>
                  <input
                    :value="settingsForm.githubAllowedLogins.join(', ')"
                    class="settings-input settings-input-wide"
                    placeholder="SuInk"
                    @input="settingsForm.githubAllowedLogins = ($event.target as HTMLInputElement).value.split(',').map((item) => item.trim()).filter(Boolean)"
                  />
                </label>

                <div class="settings-item settings-item-stacked">
                  <span>
                    <span class="settings-title">已登录会话</span>
                    <span class="settings-desc">通过 GitHub 登录的浏览器会话，撤销后该浏览器需要重新登录。</span>
                  </span>
                  <div class="grid w-full min-w-0 gap-2">
                    <p v-if="loadingSessions" class="text-sm text-[var(--muted)]">读取中…</p>
                    <p v-else-if="!githubSessions.length" class="text-sm text-[var(--muted)]">暂无 GitHub 登录会话。</p>
                    <div
                      v-for="session in githubSessions"
                      :key="session.id"
                      class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-[var(--line)] px-3 py-2"
                    >
                      <span class="min-w-0 text-sm text-[var(--ink)]">
                        <strong>{{ session.githubLogin }}</strong>
                        <span v-if="session.current" class="ml-2 text-xs text-[var(--muted)]">（当前浏览器）</span>
                        <span class="ml-2 block text-xs text-[var(--muted)]">
                          登录于 {{ formatSessionTime(session.createdAt) }} · 最近活跃 {{ formatSessionTime(session.lastSeenAt) }}
                        </span>
                      </span>
                      <button type="button" class="settings-secondary-button" @click="revokeSession(session.id)">
                        撤销
                      </button>
                    </div>
                  </div>
                </div>
              </template>
            </div>
          </section>

          <section id="settings-section-mcp" class="settings-section" data-section="mcp" v-show="sectionShown('mcp')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">MCP</h3>
            </header>
            <div class="settings-group">
              <label class="settings-item">
                <span>
                  <span class="settings-title">公网开放 MCP</span>
                  <span class="settings-desc">关闭时只有本机能连 MCP；经隧道或公网来的 /mcp 请求一律返回 404。</span>
                </span>
                <input v-model="settingsForm.exposeMcpPublicly" type="checkbox" class="settings-switch" />
              </label>
            </div>
          </section>
        </div>

        <div v-show="categoryShown('about')" class="settings-category settings-list">
          <h2 v-show="searching" class="settings-category-title">关于</h2>
          <section id="settings-section-about" class="settings-section" data-section="about" v-show="sectionShown('about')">
            <header class="settings-section-head">
              <h3 class="settings-section-title">版本与更新</h3>
            </header>
          <VersionPanel v-if="activePage === 'about' || searching" />

          <div class="settings-group">
            <div class="settings-item">
              <span>
                <span class="settings-title">开源协议</span>
                <span class="settings-desc">
                  GNU AGPL-3.0。修改后分发或部署成网络服务时，需要以同样协议公开完整源码。
                  Go 服务通过托管 aria2c 下载，内置 Google Drive 接入；可选 FFmpeg、cloudflared 遵循各自许可。
                </span>
              </span>
              <div class="flex shrink-0 gap-2">
                <button type="button" class="settings-secondary-button" @click="openExternalLink(`${SOURCE_REPOSITORY_URL}/blob/main/LICENSE`)">协议全文</button>
                <button type="button" class="settings-secondary-button" @click="openExternalLink(SOURCE_REPOSITORY_URL)">源码</button>
              </div>
            </div>
          </div>
          </section>
        </div>

        <div v-if="status?.restartRequired" class="settings-restart-note">
          监听地址或端口已修改，重启应用后新配置才会生效。当前仍监听
          {{ status.activeHost }}:{{ status.activePort }}。
        </div>

      </form>
      </div>
    </section>

    <div
      v-if="pendingRestore"
      class="fixed inset-0 z-50 grid place-items-center bg-black/55 p-4"
      @click.self="pendingRestore = null"
    >
      <section class="grid w-full max-w-lg gap-5 rounded-lg border border-[var(--line)] bg-[var(--card)] p-6 shadow-[var(--strong-shadow)]">
        <div class="grid gap-2">
          <h3 class="text-lg font-semibold text-[var(--ink)]">确认恢复备份？</h3>
          <p class="text-sm text-[var(--muted)]">
            将用 <strong class="text-[var(--ink)]">{{ pendingRestore.label }}</strong>
            整体替换当前数据库：备份之后新增的订阅、下载记录和设置都会消失。
          </p>
          <p class="text-sm text-[var(--muted)]">
            恢复前会自动把当前数据另存为一份 <code>pre-restore</code> 备份，随时可以退回来。
            恢复完成后请刷新页面；如果备份里的 token 与现在不同，还需要重新登录。
          </p>
        </div>
        <div class="flex flex-wrap justify-end gap-3">
          <button
            type="button"
            class="btn-ghost rounded-lg px-5 py-3 text-sm font-semibold"
            :disabled="restoringBackup"
            @click="pendingRestore = null"
          >
            取消
          </button>
          <button
            type="button"
            class="btn-primary rounded-lg px-5 py-3 text-sm font-semibold"
            :disabled="restoringBackup"
            @click="confirmRestore()"
          >
            {{ restoringBackup ? "恢复中…" : "确认恢复" }}
          </button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>

.provider-list {
  display: grid;
  gap: 0.5rem;
  width: 100%;
}

.provider-row {
  display: grid;
  grid-template-columns: auto minmax(6rem, 1fr) minmax(0, 1.6fr) auto auto auto;
  align-items: center;
  gap: 0.5rem;
  padding: 0.4rem 0.55rem;
  border: 1px solid var(--line);
  border-radius: 0.5rem;
  background: var(--card);
}

.metadata-platform-row {
  display: grid;
  /* 信息平台没有优先级排序，比资源来源少两列箭头 */
  grid-template-columns: auto minmax(6rem, 1fr) minmax(0, 1.6fr) auto;
  align-items: center;
  gap: 0.5rem;
  padding: 0.4rem 0.55rem;
  border: 1px solid var(--line);
  border-radius: 0.5rem;
  background: var(--card);
}

.provider-name {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--ink);
}

.provider-status {
  font-size: 0.72rem;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.provider-status-idle {
  color: var(--muted);
}

.provider-status-ok {
  color: rgb(5 150 105);
}

.provider-status-error {
  color: rgb(217 119 6);
}

.provider-button {
  padding: 0.25rem 0.55rem;
  border-radius: 0.375rem;
  font-size: 0.72rem;
  font-weight: 600;
}

/*
 * 窄屏：状态文字挪到名字下面，开关和按钮跨两行垂直居中。
 * 以前把状态单独铺满一行，后面的按钮就顺延进 1fr 那一列，「↑」被拉成一整条。
 */
@container settings-content (max-width: 30rem) {
  .provider-row {
    grid-template-columns: auto minmax(0, 1fr) auto auto auto;
    row-gap: 0;
  }

  .metadata-platform-row {
    grid-template-columns: auto minmax(0, 1fr) auto;
    row-gap: 0;
  }

  .provider-row > *,
  .metadata-platform-row > * {
    grid-row: 1 / span 2;
  }

  .provider-row > .provider-name,
  .metadata-platform-row > .provider-name {
    grid-column: 2;
    grid-row: 1;
  }

  .provider-row > .provider-status,
  .metadata-platform-row > .provider-status {
    grid-column: 2;
    grid-row: 2;
  }
}

/*
 * 设置分类与内容并排，外层保留应用导航。
 * 内容区限宽居中：设置项是一行一行读的，拉到 1400px 宽只会让左边的名字和右边的开关隔得老远。
 */
.settings-app {
  --settings-page: var(--app-bg);
  --settings-panel: transparent;
  --settings-block: var(--card);
  --settings-block-muted: var(--panel);
  --settings-control: var(--input-bg);
  display: grid;
  grid-template-columns: 15.5rem minmax(0, 1fr);
  height: 100%;
  min-height: 0;
  overflow: hidden;
  background: var(--app-bg);
}

.settings-app-narrow {
  grid-template-columns: minmax(0, 1fr);
}

.settings-sidebar {
  display: flex;
  min-height: 0;
  flex-direction: column;
  gap: 0.75rem;
  overflow: auto;
  border-right: 1px solid var(--line);
  background: var(--sidebar-bg);
  padding: 0.875rem 0.625rem 1rem;
}

.settings-app-narrow .settings-sidebar {
  border-right: none;
}

.settings-back {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  align-self: flex-start;
  border-radius: 0.375rem;
  padding: 0.375rem 0.5rem;
  color: var(--ink);
  font-size: 0.875rem;
  font-weight: 600;
}

.settings-back:hover {
  background: var(--tint-2);
}

.settings-back > svg {
  width: 1rem;
  height: 1rem;
}

.settings-back-inline {
  margin-bottom: 0.5rem;
  margin-left: -0.5rem;
  color: var(--muted);
}

.settings-sidebar-search {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  border-radius: 0.5rem;
  background: var(--tint-2);
  padding: 0 0.625rem;
}

.settings-sidebar-search:focus-within {
  box-shadow: inset 0 0 0 1px var(--accent);
}

.settings-sidebar-search > svg {
  width: 0.875rem;
  height: 0.875rem;
  flex-shrink: 0;
  color: var(--muted);
}

.settings-sidebar-search > input {
  min-width: 0;
  flex: 1;
  background: transparent;
  padding: 0.4375rem 0;
  color: var(--ink);
  font-size: 0.8125rem;
  outline: none;
}

.settings-nav {
  display: grid;
  gap: 1rem;
}

.settings-nav-group {
  display: grid;
  gap: 0.0625rem;
}

.settings-nav-group-label {
  padding: 0 0.5rem 0.25rem;
  color: var(--muted);
  font-size: 0.75rem;
}

.settings-nav-link {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.625rem;
  border-radius: 0.375rem;
  padding: 0.4375rem 0.5rem;
  color: var(--ink);
  font-size: 0.875rem;
  text-align: left;
  transition: background 120ms ease, opacity 120ms ease;
}

.settings-nav-link > svg {
  width: 1rem;
  height: 1rem;
  flex-shrink: 0;
  color: var(--muted);
}

.settings-nav-link:hover {
  background: var(--tint-2);
}

.settings-nav-link-active,
.settings-nav-link-active:hover {
  background: var(--tint-3);
  font-weight: 600;
}

.settings-nav-link-active > svg {
  color: var(--ink);
}

.settings-nav-link-dim {
  opacity: 0.45;
}

.settings-nav-count {
  border-radius: 999px;
  background: var(--accent-soft);
  padding: 0 0.4375rem;
  color: var(--accent-vivid);
  font-size: 0.6875rem;
  font-weight: 600;
}

.settings-nav-chevron {
  width: 0.875rem !important;
  height: 0.875rem !important;
}

.settings-app-narrow .settings-nav-link {
  padding: 0.625rem 0.5rem;
}

.settings-shell {
  min-height: 0;
  overflow: auto;
}

.settings-content {
  container: settings-content / inline-size;
  width: min(100%, 50rem);
  margin: 0 auto;
  padding: 2.25rem 1.5rem 4rem;
}

.settings-app-narrow .settings-content {
  padding: 1rem 1rem 3rem;
}

.settings-page-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 1.75rem;
}

.settings-page-title {
  color: var(--ink);
  font-size: 1.5rem;
  font-weight: 600;
  line-height: 1.3;
}

.settings-page-desc {
  margin-top: 0.25rem;
  color: var(--muted);
  font-size: 0.8125rem;
}

.settings-category-title {
  color: var(--muted);
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.04em;
}

.settings-single-section .settings-section-head {
  display: none !important;
}

.settings-section {
  scroll-margin-top: 1rem;
}

.settings-search-miss {
  display: none !important;
}

.settings-panel {
  display: grid;
  gap: 2rem;
}

.settings-list {
  display: grid;
  gap: 1.5rem;
}

/* 只读信息压成一块紧凑的键值区：它们是参考值，不该比要改的表单还占地方 */
.settings-summary-grid {
  display: grid;
  gap: 0;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
  border-radius: 0.5rem;
  background: var(--settings-block);
}

.settings-summary-item {
  min-width: 0;
  border-left: 1px solid color-mix(in srgb, var(--line) 60%, transparent);
  padding: 0.5rem 0.75rem;
  color: var(--ink);
}

.settings-summary-item:first-child {
  border-left: none;
}

.settings-summary-item strong {
  display: block;
  font-size: 0.8125rem;
  font-weight: 600;
}

.settings-summary-label {
  display: block;
  color: var(--muted);
  font-size: 0.6875rem;
  font-weight: 500;
}

/* 窄屏两列时补回横向分隔线 */
@media (max-width: 860px) {
  .settings-summary-item:nth-child(odd) {
    border-left: none;
  }

  .settings-summary-item:nth-child(n + 3) {
    border-top: 1px solid color-mix(in srgb, var(--line) 60%, transparent);
  }
}

/*
 * 小节 = 一行小标题 + 一张卡片，卡片里多行之间用细线分开。
 * 以前每个设置项各自一张带边框的卡片、之间 1.25rem 间距，二三十张卡片摞下来又长又碎，
 * 看不出哪几项是一伙的。
 */
.settings-section {
  display: grid;
  gap: 0.5rem;
}

.settings-section-head {
  display: grid;
  gap: 0.125rem;
  padding: 0 0.25rem;
}

.settings-section-title {
  color: var(--ink);
  font-size: 0.875rem;
  font-weight: 600;
}

.settings-section-desc {
  color: var(--muted);
  font-size: 0.75rem;
  line-height: 1.5;
}

.settings-group {
  display: grid;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--line) 60%, transparent);
  border-radius: 0.5rem;
  background: var(--settings-block);
}

.settings-group .settings-item {
  border: none;
  border-radius: 0;
  background: transparent;
}

/* 要写得比上面的 border: none 更具体，否则分隔线被它吃掉 */
.settings-group > .settings-item + .settings-item,
.settings-group > * + * {
  border-top: 1px solid color-mix(in srgb, var(--line) 55%, transparent);
}

/* 「高级」默认折叠：端口、路径、CORS 这类装好就不再碰的东西 */
.settings-advanced {
  display: grid;
  gap: 0.5rem;
}

.settings-advanced > summary {
  justify-self: start;
  cursor: pointer;
  padding: 0.125rem 0.25rem;
  color: var(--muted);
  font-size: 0.75rem;
  font-weight: 600;
  list-style: none;
}

.settings-advanced > summary::-webkit-details-marker {
  display: none;
}

.settings-advanced > summary::before {
  display: inline-block;
  margin-right: 0.25rem;
  content: "▸";
  transition: transform 120ms ease;
}

.settings-advanced[open] > summary::before {
  transform: rotate(90deg);
}

.settings-advanced > summary:hover {
  color: var(--ink);
}

.settings-item {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border: 1px solid color-mix(in srgb, var(--line) 46%, transparent);
  border-radius: 0.5rem;
  background: var(--settings-block);
  padding: 0.75rem 1rem;
}

.settings-item-stacked {
  align-items: stretch;
  flex-direction: column;
}

.settings-title {
  display: block;
  color: var(--ink);
  font-size: 0.875rem;
  font-weight: 600;
}

.settings-desc {
  display: block;
  margin-top: 0.125rem;
  color: var(--muted);
  font-size: 0.75rem;
  font-weight: 500;
  line-height: 1.5;
}

.settings-input {
  width: min(24rem, 46vw);
  min-height: 2.75rem;
  border: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
  border-radius: 0.375rem;
  background: var(--settings-control);
  padding: 0.7rem 0.9rem;
  color: var(--ink);
  outline: none;
}

.settings-input:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 16%, transparent);
}

.settings-input-short {
  width: min(12rem, 34vw);
}

/* —— 外观分组 —— */
.appearance-rows {
  display: grid;
  gap: 0.85rem;
  margin-top: 1rem;
}

.appearance-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem 1.25rem;
}

.appearance-label {
  color: var(--muted);
  font-size: 0.8125rem;
  font-weight: 600;
}

.appearance-seg {
  display: inline-flex;
  gap: 2px;
  border: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
  border-radius: 0.5rem;
  background: var(--panel);
  padding: 3px;
}

.appearance-seg-item {
  display: inline-flex;
  align-items: center;
  height: 1.75rem;
  border: none;
  border-radius: 0.375rem;
  background: transparent;
  padding: 0 0.75rem;
  color: var(--muted);
  font-size: 0.75rem;
  font-weight: 600;
}

.appearance-seg-item.is-on {
  background: var(--card);
  box-shadow: var(--soft-shadow);
  color: var(--ink);
}

.appearance-accent {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 0.6rem;
}

.appearance-presets,
.appearance-custom {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: center;
  gap: 0.5rem;
}

.appearance-chip {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  height: 2.125rem;
  border: 1px solid color-mix(in srgb, var(--line) 90%, transparent);
  border-radius: 999px;
  background: var(--card);
  padding: 0 0.8rem 0 0.55rem;
  color: var(--ink);
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
}

.appearance-chip.is-on {
  border: 2px solid var(--chip-color, var(--accent));
  background: color-mix(in srgb, var(--chip-color, var(--accent)) 10%, transparent);
  color: var(--chip-color, var(--accent));
}

.appearance-chip-custom {
  border-style: dashed;
  border-color: color-mix(in srgb, var(--ink) 30%, transparent);
}

.appearance-dot {
  width: 1rem;
  height: 1rem;
  border-radius: 999px;
  background: var(--chip-color, var(--accent));
}

.appearance-dot-rainbow {
  background: conic-gradient(#e0578f, #c77d13, #14935f, #0041fd, #7c5cff, #e0578f);
}

/* 原生取色器铺满整块 chip：点哪儿都能开，但看不见它自己那副长相 */
.appearance-color-input {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  border: none;
  padding: 0;
  opacity: 0;
  cursor: pointer;
}

.appearance-hex {
  width: 7.5rem;
  min-height: 2.125rem;
  padding: 0.35rem 0.65rem;
  font-family: "SF Mono", Menlo, monospace;
  font-size: 0.75rem;
  text-transform: uppercase;
}

.appearance-reset {
  border: none;
  background: none;
  color: var(--muted);
  font-size: 0.75rem;
  font-weight: 600;
  text-decoration: underline;
}

.appearance-preview {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
}

.appearance-preview-primary,
.appearance-preview-ghost,
.appearance-preview-selected {
  display: inline-flex;
  align-items: center;
  height: 2.125rem;
  border-radius: 0.5rem;
  padding: 0 0.85rem;
  font-size: 0.8125rem;
  font-weight: 600;
}

.appearance-preview-primary {
  background: var(--accent);
  color: var(--accent-contrast);
}

.appearance-preview-ghost {
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink);
}

.appearance-preview-selected {
  height: 1.875rem;
  border: 1px solid var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 0.75rem;
}

.appearance-preview-bar {
  display: block;
  overflow: hidden;
  width: 7.5rem;
  height: 6px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--ink) 12%, transparent);
}

.appearance-preview-bar > span {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--accent-vivid);
}

.settings-input-wide {
  width: 100%;
}

.settings-combo {
  display: grid;
  width: min(24rem, 46vw);
  grid-template-columns: minmax(0, 1fr) 8rem;
  gap: 0.75rem;
}

.settings-combo .settings-input {
  width: 100%;
}

.settings-switch {
  position: relative;
  width: 3rem;
  height: 1.75rem;
  flex: 0 0 auto;
  cursor: pointer;
  appearance: none;
  border-radius: 0.375rem;
  background: color-mix(in srgb, var(--muted) 45%, transparent);
  transition: background 160ms ease;
}

.settings-switch::before {
  position: absolute;
  top: 0.25rem;
  left: 0.25rem;
  width: 1.25rem;
  height: 1.25rem;
  content: "";
  border-radius: 0.375rem;
  background: white;
  transition: transform 160ms ease;
}

.settings-switch:checked {
  background: var(--accent);
}

.settings-switch:checked::before {
  transform: translateX(1.25rem);
}

.settings-segmented {
  display: grid;
  width: min(24rem, 46vw);
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.25rem;
  border: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
  border-radius: 0.75rem;
  background: var(--settings-control);
  padding: 0.25rem;
}

.settings-segmented-button {
  min-height: 2.35rem;
  border-radius: 0.55rem;
  padding: 0 0.9rem;
  color: var(--muted);
  font-size: 0.875rem;
  font-weight: 600;
  transition:
    background 160ms ease,
    color 160ms ease,
    box-shadow 160ms ease;
}

.settings-segmented-button:hover {
  color: var(--ink);
}

.settings-segmented-button-active,
.settings-segmented-button-active:hover {
  background: var(--accent);
  color: var(--accent-contrast);
}

.settings-combo-stacked {
  flex-direction: column;
  align-items: flex-end;
  gap: 0.35rem;
}

.settings-combo-row {
  display: flex;
  gap: 0.5rem;
}

/* 一行两个输入框（地址+端口、用户名+密码）：总宽和单个输入框一样，两个框分着用 */
.settings-pair {
  display: flex;
  width: min(24rem, 46vw);
  flex-shrink: 0;
  gap: 0.5rem;
}

.settings-pair .settings-input {
  width: auto;
  min-width: 0;
  flex: 1 1 0;
}

.settings-pair .settings-input-short {
  flex: 0 0 6.5rem;
}

.settings-input-invalid,
.settings-input-invalid:focus {
  border-color: var(--err);
  box-shadow: 0 0 0 3px var(--err-soft);
}

.settings-field-error {
  color: var(--err);
  font-size: 0.75rem;
  font-weight: 600;
}

.settings-restart-note {
  border: 1px solid color-mix(in srgb, var(--warn) 35%, transparent);
  border-radius: 1rem;
  background: var(--warn-soft);
  padding: 0.75rem 1rem;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--warn);
}

.settings-primary-button,
.settings-secondary-button,
.settings-icon-button {
  min-height: 2.75rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  font-weight: 600;
  transition:
    opacity 160ms ease,
    background 160ms ease,
    transform 160ms ease;
}

.settings-primary-button {
  background: var(--accent);
  padding: 0 1.75rem;
  color: var(--accent-contrast);
}

.settings-secondary-button {
  border: 1px solid color-mix(in srgb, var(--line) 65%, transparent);
  background: var(--settings-block);
  padding: 0 1.1rem;
  color: var(--ink);
}

.settings-icon-button {
  width: 2.75rem;
  border: 1px solid color-mix(in srgb, var(--line) 65%, transparent);
  background: var(--settings-block);
  color: var(--muted);
  font-size: 1.25rem;
}

.settings-primary-button:hover,
.settings-secondary-button:hover,
.settings-icon-button:hover {
  transform: translateY(-1px);
}

.settings-primary-button:disabled,
.settings-secondary-button:disabled,
.settings-icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
  transform: none;
}

.secret-mask-input {
  -webkit-text-security: disc;
}

.tunnel-checks {
  display: grid;
  gap: 0.5rem;
}

.tunnel-check {
  display: flex;
  gap: 0.5rem;
  border-radius: 0.5rem;
  padding: 0.6rem 0.85rem;
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1.5;
}

.tunnel-check strong {
  flex: 0 0 auto;
}

.tunnel-check-block {
  border: 1px solid color-mix(in srgb, var(--err) 35%, transparent);
  background: var(--err-soft);
  color: var(--err);
}

.tunnel-check-warn {
  border: 1px solid color-mix(in srgb, var(--warn) 35%, transparent);
  background: var(--warn-soft);
  color: var(--warn);
}

.tunnel-check-pass {
  border: 1px solid color-mix(in srgb, var(--line) 60%, transparent);
  background: var(--settings-block);
  color: var(--muted);
}

.tunnel-check-fix {
  margin-left: auto;
  flex: 0 0 auto;
  border-radius: 0.375rem;
  border: 1px solid currentColor;
  padding: 0.15rem 0.6rem;
  font-size: 0.75rem;
  font-weight: 600;
}


.tunnel-discovered {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.tunnel-discovered-item {
  padding: 0 0.9rem;
}

.tunnel-logs {
  border: 1px solid color-mix(in srgb, var(--line) 46%, transparent);
  border-radius: 0.5rem;
  background: var(--settings-block);
  padding: 0.75rem 1rem;
}

.tunnel-logs summary {
  cursor: pointer;
  color: var(--ink);
  font-size: 0.875rem;
  font-weight: 600;
}

.tunnel-log-list {
  display: grid;
  max-height: 18rem;
  gap: 0.25rem;
  overflow: auto;
  margin-top: 0.75rem;
  font-size: 0.75rem;
  line-height: 1.5;
}

.tunnel-log-list li {
  display: flex;
  gap: 0.6rem;
  color: var(--muted);
}

.tunnel-log-list code {
  flex: 0 0 auto;
  opacity: 0.7;
}

.tunnel-log-warn {
  color: var(--warn);
}

.tunnel-log-error {
  color: var(--err);
}

@media (max-width: 860px) {
  .settings-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

}

.update-badge {
  border-radius: 999px;
  padding: 0.125rem 0.5rem;
  font-size: 0.75rem;
}

.update-badge-ok {
  background: var(--ok-soft);
  color: var(--ok);
}

.update-badge-warn {
  background: var(--warn-soft);
  color: var(--warn);
}

.update-badge-err {
  background: var(--err-soft);
  color: var(--err);
}

.update-badge-accent {
  background: color-mix(in srgb, var(--accent) 18%, transparent);
  color: var(--accent);
}

.update-badge-muted {
  color: var(--muted);
}

.update-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.update-progress {
  display: grid;
  gap: 0.375rem;
  border-radius: 0.5rem;
  background: var(--settings-block-muted);
  padding: 0.625rem 0.75rem;
}

.update-progress-label {
  display: flex;
  justify-content: space-between;
  font-size: 0.8125rem;
  color: var(--muted);
}

.update-progress-track {
  height: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: color-mix(in srgb, var(--line) 70%, transparent);
}

.update-progress-track > span {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--accent);
  transition: width 200ms ease;
}

.update-note {
  font-size: 0.8125rem;
  line-height: 1.6;
  color: var(--muted);
}

.update-note-warn {
  color: var(--warn);
}

.update-history summary {
  cursor: pointer;
  font-size: 0.875rem;
  font-weight: 600;
}

.update-history-list {
  display: grid;
  gap: 0.75rem;
  margin-top: 0.625rem;
}

.update-history-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8125rem;
  color: var(--muted);
}

.update-history-notes {
  margin-top: 0.25rem;
  max-height: 12rem;
  overflow: auto;
  border-radius: 0.375rem;
  background: var(--settings-block-muted);
  padding: 0.5rem 0.625rem;
  font-size: 0.75rem;
  white-space: pre-wrap;
}

/*
 * —— 信息密度 ——
 * 以前一屏只放得下三四个设置：大号开关、每项上下 1rem 内边距、文本框独占一行、
 * 列表里每一行再套一个带边框的小框，窗口一窄（<860px 视口）连开关都被挤到文字下面。
 * 现在：控件缩一号；开关、下拉这类短控件始终靠右，放不下才自动折到下一行；
 * 设置区够宽时，文本框也和标题并排；列表行去掉自己的框，改用细分隔线。
 */
.settings-list {
  gap: 1.25rem;
}

.settings-section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  column-gap: 0.625rem;
  row-gap: 0.125rem;
}

.settings-item {
  flex-wrap: wrap;
  gap: 0.5rem 1rem;
  padding: 0.625rem 0.875rem;
}

/* 标题+说明那一块吃掉剩余宽度；控件保持自身宽度，行里放不下时整体折到下一行 */
.settings-item:not(.settings-item-stacked) > span:first-child {
  min-width: 0;
  flex: 1 1 14rem;
}

.settings-switch {
  width: 2.25rem;
  height: 1.25rem;
  border-radius: 999px;
}

.settings-switch::before {
  top: 0.1875rem;
  left: 0.1875rem;
  width: 0.875rem;
  height: 0.875rem;
  border-radius: 999px;
}

.settings-switch:checked::before {
  transform: translateX(1rem);
}

.settings-input {
  min-height: 2.125rem;
  padding: 0.375rem 0.75rem;
  font-size: 0.8125rem;
}

.provider-list {
  gap: 0;
}

.provider-row,
.metadata-platform-row {
  border: none;
  border-radius: 0;
  background: transparent;
  padding: 0.375rem 0;
}

.provider-row + .provider-row,
.metadata-platform-row + .metadata-platform-row {
  border-top: 1px solid color-mix(in srgb, var(--line) 55%, transparent);
}

.provider-name {
  font-size: 0.8125rem;
}

.provider-button {
  padding: 0.125rem 0.5rem;
}

/* 分段控件按内容收宽，别按视口占掉半行；按钮统一缩到和输入框同高 */
.settings-segmented {
  width: auto;
  border-radius: 0.5rem;
  padding: 0.1875rem;
}

.settings-segmented-button {
  min-height: 1.75rem;
  border-radius: 0.375rem;
  padding: 0 0.75rem;
  font-size: 0.8125rem;
}

.settings-primary-button,
.settings-secondary-button,
.settings-icon-button {
  min-height: 2.125rem;
  font-size: 0.8125rem;
}

/* 设置区够宽时，「标题 + 说明 / 文本框」并排成一行，而不是摞三行 */
@container settings-content (min-width: 40rem) {
  .settings-item-stacked:has(> .settings-input) {
    flex-direction: row;
    flex-wrap: nowrap;
    align-items: center;
  }

  .settings-item-stacked:has(> .settings-input) > span:first-child {
    min-width: 0;
    flex: 1 1 auto;
  }

  .settings-item-stacked:has(> .settings-input) > .settings-input {
    width: min(26rem, 52%);
    flex: 0 0 auto;
  }
}

/* 窄设置区：并排的两个输入框、分段控件不再按视口算宽度，直接占满一行 */
@container settings-content (max-width: 30rem) {
  .settings-pair,
  .settings-combo {
    width: 100%;
  }

  .settings-input:not(.settings-input-short) {
    width: 100%;
  }
}
/* Diana 式嵌套设置布局：保留应用顶栏与主导航。 */
.settings-app { grid-template-columns:190px minmax(0,1fr); gap:20px; align-items:start; overflow:auto; padding:0; }
.settings-sidebar { position:sticky; top:0; padding:4px 0 16px; border:0; background:transparent; overflow:visible; }
.settings-nav { gap:16px; }.settings-nav-group { gap:2px; }
.settings-nav-group-label { font-size:11.5px; padding:0 10px 5px; }
.settings-nav-link { padding:8px 10px; border-radius:12px; font-size:13.5px; min-height:36px; }
.settings-nav-link-active, .settings-nav-link-active:hover { background:var(--accent-soft); color:var(--accent); }
.settings-nav-link-active > svg { color:inherit; }
.settings-shell { overflow:visible; min-width:0; }
.settings-content { width:100%; max-width:1000px; margin:0; padding:0 0 40px; }
.settings-page-head { margin-bottom:16px; gap:12px; flex-wrap:wrap; }
.settings-page-title { font-size:16px; line-height:1.4; }
.settings-group { border-radius:14px; }
.settings-sidebar-search { border:1px solid var(--line); background:var(--input-bg); }
@media(max-width:640px) {
 .settings-app { grid-template-columns:minmax(0,1fr); gap:18px; }
 .settings-sidebar { position:static; padding:0; }
 .settings-nav { display:flex; flex-wrap:wrap; gap:6px; }
 .settings-nav-group { display:contents; }.settings-nav-group-label { display:none; }
 .settings-nav-link { width:auto; min-height:40px; padding:8px 10px; font-size:12px; }
 .settings-nav-link svg { display:none; }
 .settings-content { padding:0 0 32px; }
}
</style>
