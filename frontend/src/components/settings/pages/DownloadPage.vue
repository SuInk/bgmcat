<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref, watch } from "vue";

import type { AppSettings, CloudProviderType, DownloadClientType } from "@shared/index";

import {
  connectCloud,
  createCloudFolder,
  disconnectCloud,
  listCloudFolders,
  listCloudProviders,
  syncPublicTrackers,
  testDownloadEngine,
  type CloudFolder,
  type CloudProviderStatus,
  type DownloadEngineTestResult
} from "../../../api";
import SegmentedControl from "../../common/SegmentedControl.vue";
import AppNavIcon from "../../layout/AppNavIcon.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { settingsOpenProbeKey } from "../context";
import { copyText, openExternalLink } from "../../../lib/actions";
import {
  cloudDriveConnected,
  isDesktopShell,
  refreshSettings,
  refreshStatus,
  saveSettingsFields,
  settingsForm,
  settingsSaveError
} from "../../../stores/session";
import { toastError, updateFeedback } from "../../../stores/toasts";

type StorageMode = "local" | "both" | "cloud";

const STORAGE_MODE_OPTIONS = [
  { value: "local", label: "本机" },
  { value: "both", label: "本机 + 云盘" },
  { value: "cloud", label: "仅云盘" }
] as const satisfies ReadonlyArray<{ value: StorageMode; label: string }>;

const CLOUD_PROVIDER_OPTIONS = [
  { value: "google", label: "Google Drive" },
  { value: "onedrive", label: "OneDrive" },
  { value: "webdav", label: "WebDAV" }
] as const satisfies ReadonlyArray<{ value: CloudProviderType; label: string }>;

const DOWNLOAD_CLIENT_OPTIONS = [
  { value: "native", label: "内置引擎" },
  { value: "qbittorrent", label: "qBittorrent" },
  { value: "aria2-rpc", label: "Aria2 RPC" },
  { value: "transmission", label: "Transmission" }
] as const satisfies ReadonlyArray<{ value: DownloadClientType; label: string }>;

const openProbe = inject(settingsOpenProbeKey, () => undefined);

const storageMode = computed<StorageMode>({
  get: () => {
    if (settingsForm.defaultSaveTo === "cloud" || (settingsForm.cloudArchiveEnabled && settingsForm.cloudDeleteLocal)) {
      return "cloud";
    }
    if (settingsForm.cloudArchiveEnabled) {
      return "both";
    }
    return "local";
  },
  set: (mode: StorageMode) => {
    if (mode === "local") {
      settingsForm.defaultSaveTo = "local";
      settingsForm.cloudArchiveEnabled = false;
      settingsForm.cloudDeleteLocal = false;
    } else if (mode === "both") {
      settingsForm.defaultSaveTo = "local";
      settingsForm.cloudArchiveEnabled = true;
      settingsForm.cloudDeleteLocal = false;
    } else {
      settingsForm.defaultSaveTo = "cloud";
      settingsForm.cloudArchiveEnabled = true;
      settingsForm.cloudDeleteLocal = true;
    }
  }
});

const cloudProvider = computed<CloudProviderType>({
  get: () => settingsForm.cloudProvider || "google",
  set: (next: CloudProviderType) => {
    settingsForm.cloudProvider = next;
    oauthError.value = "";
    folderPickerOpen.value = false;
    selectedFolderName.value = "";
  }
});

const downloadClient = computed<DownloadClientType>({
  get: () => settingsForm.downloadClient,
  set: (client: DownloadClientType) => {
    settingsForm.downloadClient = client;
    settingsForm.qbittorrentEnabled = client === "qbittorrent";
    engineTestResult.value = null;
  }
});

/* ---------- 多云盘状态与授权 ---------- */
const providerMap = ref<Record<CloudProviderType, CloudProviderStatus>>({
  google: { provider: "google", displayName: "Google Drive", authKind: "oauth", connected: false, configured: false, builtin: false, folderId: "root" },
  onedrive: { provider: "onedrive", displayName: "Microsoft OneDrive", authKind: "oauth", connected: false, configured: false, builtin: false, folderId: "root" },
  webdav: { provider: "webdav", displayName: "WebDAV", authKind: "basic", connected: false, configured: false, builtin: false, folderId: "Bangumi Cat" }
});

const activeProviderStatus = computed(() => providerMap.value[cloudProvider.value]);
const cloudConnected = computed(() => Boolean(activeProviderStatus.value?.connected));
const builtinCredentials = computed(() => Boolean(activeProviderStatus.value?.builtin));

const authBusy = ref(false);
const oauthError = ref("");
const waitingExternalAuth = ref(false);
const jsonInputRef = ref<HTMLInputElement | null>(null);

const folderPickerOpen = ref(false);
const folderBusy = ref(false);
const folderTrail = ref<CloudFolder[]>([{ id: "root", name: "根目录" }]);
const folderList = ref<CloudFolder[]>([]);
const folderNextToken = ref("");
const selectedFolderName = ref("");

let externalPollTimer: ReturnType<typeof setInterval> | null = null;
let externalPollExpires = 0;

const isLoopbackOrigin = computed(() => {
  const host = window.location.hostname;
  return host === "127.0.0.1" || host === "localhost" || host === "::1" || host === "[::1]";
});

const defaultWebCallback = `${window.location.origin}/api/cloud/callback`;

function effectiveSavedRedirectUri(rawUri: string): string | undefined {
  const raw = rawUri.trim();
  if (!raw) {
    return undefined;
  }
  if (isLoopbackOrigin.value) {
    try {
      const u = new URL(raw);
      const isLoop =
        u.hostname === "127.0.0.1" ||
        u.hostname === "localhost" ||
        u.hostname === "::1" ||
        u.hostname === "[::1]";
      if (isLoop && u.host !== window.location.host) {
        return undefined;
      }
    } catch {
      return undefined;
    }
  }
  return raw;
}

const hasGoogleCredentials = computed(
  () => Boolean(providerMap.value.google.builtin || settingsForm.googleClientId.trim())
);

const hasOneDriveCredentials = computed(
  () => Boolean(providerMap.value.onedrive.builtin || settingsForm.onedriveClientId.trim())
);

function rootLabelForProvider(provider: CloudProviderType): string {
  if (provider === "onedrive") return "OneDrive";
  if (provider === "webdav") return "根目录";
  return "我的云盘";
}

const activeFolderSettingKey = computed<keyof AppSettings>(() => {
  if (cloudProvider.value === "onedrive") return "onedriveFolderId";
  if (cloudProvider.value === "webdav") return "webdavRootPath";
  return "cloudDriveFolderId";
});

const activeFolderValue = computed({
  get: () => String(settingsForm[activeFolderSettingKey.value] ?? "").trim(),
  set: (val: string) => {
    (settingsForm as Record<string, unknown>)[activeFolderSettingKey.value] = val;
  }
});

const currentFolderDisplay = computed(() => {
  const raw = activeFolderValue.value;
  if (!raw || raw === "root") {
    return rootLabelForProvider(cloudProvider.value);
  }
  return selectedFolderName.value || raw;
});

function stopExternalPolling(): void {
  if (externalPollTimer !== null) {
    clearInterval(externalPollTimer);
    externalPollTimer = null;
  }
  waitingExternalAuth.value = false;
}

async function refreshCloud(): Promise<void> {
  try {
    const items = await listCloudProviders();
    const prevConnected = cloudConnected.value;
    const next = { ...providerMap.value };
    for (const item of items) {
      next[item.provider] = item;
    }
    providerMap.value = next;
    cloudDriveConnected.value = Boolean(next[cloudProvider.value]?.connected);

    if (cloudConnected.value && !prevConnected && waitingExternalAuth.value) {
      stopExternalPolling();
      if (storageMode.value === "local") {
        storageMode.value = "both";
        await saveSettingsFields(["defaultSaveTo", "cloudArchiveEnabled", "cloudDeleteLocal"]);
      }
      updateFeedback(`${activeProviderStatus.value.displayName} 已连接`, "success");
    }
  } catch {
    // 未登录时由登录页处理
  }
}

watch(cloudProvider, () => {
  cloudDriveConnected.value = Boolean(providerMap.value[cloudProvider.value]?.connected);
});

function consumeCallbackQueryParams(): void {
  const params = new URLSearchParams(window.location.search);
  const cloudParam = params.get("cloud");
  const cloudErr = params.get("cloudError");
  const cbProvider = params.get("provider") as CloudProviderType | null;
  if (!cloudParam && !cloudErr) {
    return;
  }
  params.delete("cloud");
  params.delete("cloudError");
  params.delete("provider");
  const nextQuery = params.toString();
  const cleanUrl = `${window.location.pathname}${nextQuery ? `?${nextQuery}` : ""}${window.location.hash}`;
  window.history.replaceState(null, "", cleanUrl);

  const label = cbProvider === "onedrive" ? "Microsoft OneDrive" : "Google Drive";
  if (cloudErr) {
    oauthError.value = cloudErr;
    updateFeedback(`${label} 授权未完成：${cloudErr}`, "error");
    return;
  }
  if (cloudParam === "connected") {
    oauthError.value = "";
    void refreshSettings();
    void refreshCloud();
    updateFeedback(`${label} 已连接`, "success");
  }
}

async function saveManualGoogleAndAuthorize(): Promise<void> {
  settingsForm.googleRedirectUri = isLoopbackOrigin.value ? "" : defaultWebCallback;
  await startOAuth("google", settingsForm.googleRedirectUri || undefined, true);
}

async function saveManualOneDriveAndAuthorize(): Promise<void> {
  settingsForm.onedriveRedirectUri = defaultWebCallback;
  await startOAuth("onedrive", defaultWebCallback, true);
}

async function startOAuth(
  provider: "google" | "onedrive",
  overrideRedirectUri?: string,
  forceSave = false
): Promise<void> {
  if (authBusy.value) {
    return;
  }
  oauthError.value = "";
  authBusy.value = true;
  try {
    if (provider === "google" && !providerMap.value.google.builtin) {
      const cid = settingsForm.googleClientId.trim();
      if (!cid) {
        authBusy.value = false;
        return;
      }
      settingsForm.googleClientId = cid;
      settingsForm.googleClientSecret = settingsForm.googleClientSecret.trim();
      if (overrideRedirectUri !== undefined) {
        settingsForm.googleRedirectUri = overrideRedirectUri;
      }
      await saveSettingsFields(
        forceSave || overrideRedirectUri !== undefined
          ? ["cloudProvider", "googleClientId", "googleClientSecret", "googleRedirectUri"]
          : ["cloudProvider", "googleClientId", "googleClientSecret"]
      );
      if (settingsSaveError.value) {
        throw new Error(settingsSaveError.value);
      }
    } else if (provider === "onedrive" && !providerMap.value.onedrive.builtin) {
      const cid = settingsForm.onedriveClientId.trim();
      if (!cid) {
        authBusy.value = false;
        return;
      }
      settingsForm.onedriveClientId = cid;
      settingsForm.onedriveClientSecret = settingsForm.onedriveClientSecret.trim();
      if (overrideRedirectUri !== undefined) {
        settingsForm.onedriveRedirectUri = overrideRedirectUri;
      }
      await saveSettingsFields(
        forceSave || overrideRedirectUri !== undefined
          ? ["cloudProvider", "onedriveClientId", "onedriveClientSecret", "onedriveRedirectUri"]
          : ["cloudProvider", "onedriveClientId", "onedriveClientSecret"]
      );
      if (settingsSaveError.value) {
        throw new Error(settingsSaveError.value);
      }
    }

    const redirectUri =
      overrideRedirectUri ||
      (provider === "onedrive"
        ? settingsForm.onedriveRedirectUri.trim() || defaultWebCallback
        : effectiveSavedRedirectUri(settingsForm.googleRedirectUri));

    if (isDesktopShell) {
      const result = await connectCloud({ provider, redirectUri });
      if (result.url) {
        openExternalLink(result.url);
      }
      waitingExternalAuth.value = true;
      externalPollExpires = Date.now() + 10 * 60 * 1000;
      if (externalPollTimer !== null) clearInterval(externalPollTimer);
      externalPollTimer = setInterval(() => {
        if (Date.now() > externalPollExpires) {
          stopExternalPolling();
        } else {
          void refreshCloud();
        }
      }, 2500);
      authBusy.value = false;
      return;
    }

    const result = await connectCloud({
      provider,
      redirectUri,
      returnTo: `/service?cloud=connected&provider=${provider}`
    });
    if (result.url) {
      window.location.assign(result.url);
    }
  } catch (error) {
    authBusy.value = false;
    oauthError.value = error instanceof Error ? error.message : String(error);
    toastError(error, "发起授权失败");
  }
}

async function handleConnectWebDAV(): Promise<void> {
  if (authBusy.value) return;
  oauthError.value = "";
  authBusy.value = true;
  try {
    await connectCloud({
      provider: "webdav",
      webdavUrl: settingsForm.webdavUrl.trim(),
      webdavUsername: settingsForm.webdavUsername.trim(),
      webdavPassword: settingsForm.webdavPassword,
      webdavRootPath: settingsForm.webdavRootPath.trim()
    });
    await refreshSettings();
    await refreshCloud();
    if (storageMode.value === "local") {
      storageMode.value = "both";
      await saveSettingsFields(["defaultSaveTo", "cloudArchiveEnabled", "cloudDeleteLocal"]);
    }
    updateFeedback("WebDAV 已连接", "success");
  } catch (error) {
    oauthError.value = error instanceof Error ? error.message : String(error);
    toastError(error, "连接 WebDAV 失败");
  } finally {
    authBusy.value = false;
  }
}

async function handleImportJsonAndAuthorize(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) {
    return;
  }
  oauthError.value = "";
  try {
    if (file.size > 65536) {
      throw new Error("凭据文件过大，请选择从 Google Cloud 下载的 OAuth JSON 文件。");
    }
    let parsed: Record<string, unknown>;
    try {
      parsed = JSON.parse(await file.text()) as Record<string, unknown>;
    } catch {
      throw new Error("不是有效的 JSON 文件。");
    }

    const installed = parsed.installed as Record<string, unknown> | undefined;
    const web = parsed.web as Record<string, unknown> | undefined;
    const block = installed ?? web;
    const clientId = typeof block?.client_id === "string" ? block.client_id.trim() : "";
    const clientSecret = typeof block?.client_secret === "string" ? block.client_secret.trim() : "";

    if (!clientId.endsWith(".apps.googleusercontent.com") || !clientSecret) {
      throw new Error("未识别到有效的 OAuth Client ID / Secret，请选择「桌面应用」或「Web 应用」JSON。");
    }

    let resolvedRedirect = "";
    if (installed) {
      resolvedRedirect = isLoopbackOrigin.value ? "" : defaultWebCallback;
    } else if (web) {
      resolvedRedirect = defaultWebCallback;
      if (Array.isArray(web.redirect_uris)) {
        const uris = web.redirect_uris.filter((u): u is string => typeof u === "string");
        const exactCallback = uris.find((u) => u === defaultWebCallback);
        const sameOrigin = uris.find((u) => u.startsWith(window.location.origin));
        resolvedRedirect = exactCallback ?? sameOrigin ?? defaultWebCallback;
      }
    }

    settingsForm.cloudProvider = "google";
    settingsForm.googleClientId = clientId;
    settingsForm.googleClientSecret = clientSecret;
    settingsForm.googleRedirectUri = resolvedRedirect;

    await startOAuth("google", resolvedRedirect, true);
  } catch (error) {
    oauthError.value = error instanceof Error ? error.message : String(error);
    toastError(error, "导入 OAuth JSON 失败");
  }
}

async function handleDisconnect(): Promise<void> {
  authBusy.value = true;
  const targetProvider = cloudProvider.value;
  const label = activeProviderStatus.value.displayName;
  try {
    stopExternalPolling();
    await disconnectCloud(targetProvider);
    storageMode.value = "local";
    folderPickerOpen.value = false;
    await refreshCloud();
    updateFeedback(`已断开 ${label}`, "info");
  } catch (error) {
    toastError(error, "断开连接失败");
  } finally {
    authBusy.value = false;
  }
}

function openCloudDirectProbe(): void {
  if (cloudProvider.value === "google") {
    openProbe("google");
    return;
  }
  if (cloudProvider.value === "onedrive") {
    openProbe("onedrive");
    return;
  }
  try {
    const u = new URL(settingsForm.webdavUrl.trim());
    if (u.protocol === "https:" && u.hostname) {
      openProbe(`custom-${u.hostname.toLowerCase()}`, u.hostname.toLowerCase(), "WebDAV");
      return;
    }
  } catch {
    // fallback below
  }
  openProbe("google");
}

/* ---------- 云盘目录选择器 ---------- */
const currentPickerFolder = computed(() => folderTrail.value[folderTrail.value.length - 1]!);

async function loadPickerFolders(append = false): Promise<void> {
  folderBusy.value = true;
  try {
    const res = await listCloudFolders(
      currentPickerFolder.value.id,
      append ? folderNextToken.value : "",
      cloudProvider.value
    );
    folderList.value = append ? [...folderList.value, ...res.files] : res.files;
    folderNextToken.value = res.nextPageToken || "";
  } catch (error) {
    toastError(error, "读取云盘目录失败");
  } finally {
    folderBusy.value = false;
  }
}

async function toggleFolderPicker(): Promise<void> {
  folderPickerOpen.value = !folderPickerOpen.value;
  if (folderPickerOpen.value) {
    folderTrail.value = [{ id: "root", name: rootLabelForProvider(cloudProvider.value) }];
    await loadPickerFolders(false);
  }
}

async function enterPickerFolder(folder: CloudFolder): Promise<void> {
  folderTrail.value = [...folderTrail.value, folder];
  await loadPickerFolders(false);
}

async function stepUpPickerFolder(): Promise<void> {
  if (folderTrail.value.length <= 1) return;
  folderTrail.value = folderTrail.value.slice(0, -1);
  await loadPickerFolders(false);
}

async function applyArchiveFolder(folder: CloudFolder): Promise<void> {
  const key = activeFolderSettingKey.value;
  activeFolderValue.value = cloudProvider.value === "webdav" && folder.id === "root" ? "" : folder.id;
  selectedFolderName.value = folder.id === "root" ? "" : folder.name;
  await saveSettingsFields([key]);
  folderPickerOpen.value = false;
  updateFeedback(`已设为「${folder.name}」`, "success");
}

async function createAndApplyBangumiFolder(): Promise<void> {
  folderBusy.value = true;
  try {
    const created = await createCloudFolder(currentPickerFolder.value.id, cloudProvider.value);
    await applyArchiveFolder(created);
  } catch (error) {
    toastError(error, "新建文件夹失败");
  } finally {
    folderBusy.value = false;
  }
}

/* ---------- 下载引擎测试 ---------- */
const engineTesting = ref(false);
const engineTestResult = ref<DownloadEngineTestResult | null>(null);

async function handleTestEngine(): Promise<void> {
  if (engineTesting.value) return;
  engineTesting.value = true;
  try {
    const client = settingsForm.downloadClient;
    if (client === "qbittorrent") {
      await saveSettingsFields(["downloadClient", "qbittorrentEnabled", "qbittorrentBaseUrl", "qbittorrentUsername", "qbittorrentPassword"]);
    } else if (client === "aria2-rpc") {
      await saveSettingsFields(["downloadClient", "aria2RpcUrl", "aria2RpcSecret"]);
    } else if (client === "transmission") {
      await saveSettingsFields(["downloadClient", "transmissionRpcUrl", "transmissionUsername", "transmissionPassword"]);
    }
    const res = await testDownloadEngine({
      client,
      qbittorrentBaseUrl: settingsForm.qbittorrentBaseUrl,
      qbittorrentUsername: settingsForm.qbittorrentUsername,
      qbittorrentPassword: settingsForm.qbittorrentPassword,
      aria2RpcUrl: settingsForm.aria2RpcUrl,
      aria2RpcSecret: settingsForm.aria2RpcSecret,
      transmissionRpcUrl: settingsForm.transmissionRpcUrl,
      transmissionUsername: settingsForm.transmissionUsername,
      transmissionPassword: settingsForm.transmissionPassword
    });
    engineTestResult.value = res;
    void refreshStatus();
  } catch (error) {
    toastError(error, "测试下载引擎失败");
  } finally {
    engineTesting.value = false;
  }
}

/* ---------- 额外 Tracker ---------- */
const RECOMMENDED_TRACKERS = [
  "udp://tracker.opentrackr.org:1337/announce",
  "udp://open.stealth.si:80/announce",
  "udp://tracker.torrent.eu.org:451/announce",
  "udp://exodus.desync.com:6969/announce",
  "http://nyaa.tracker.wf:7777/announce"
];
const TRACKER_SCHEMES = ["udp:", "http:", "https:", "wss:"];

const trackerDraft = ref("");
const invalidTrackers = ref<string[]>([]);
const syncingTrackers = ref(false);
const trackerSyncHint = ref("");

watch(
  () => settingsForm.btTrackers,
  (trackers) => {
    trackerDraft.value = (trackers ?? []).join("\n");
  },
  { immediate: true }
);

function isValidTracker(value: string): boolean {
  try {
    const url = new URL(value);
    return TRACKER_SCHEMES.includes(url.protocol) && Boolean(url.host);
  } catch {
    return false;
  }
}

function commitTrackers(): void {
  const lines = [...new Set(trackerDraft.value.split(/[\s,]+/u).map((line) => line.trim()).filter(Boolean))];
  invalidTrackers.value = lines.filter((line) => !isValidTracker(line));
  const valid = lines.filter(isValidTracker);
  if (valid.join("\n") !== (settingsForm.btTrackers ?? []).join("\n")) {
    settingsForm.btTrackers = valid;
  }
  if (invalidTrackers.value.length) {
    trackerDraft.value = [...valid, ...invalidTrackers.value].join("\n");
  }
}

function fillRecommendedTrackers(): void {
  const merged = [...new Set([...(settingsForm.btTrackers ?? []), ...RECOMMENDED_TRACKERS])];
  settingsForm.btTrackers = merged;
  invalidTrackers.value = [];
  void saveSettingsFields(["btTrackers"]);
}

async function handleSyncPublicTrackers(): Promise<void> {
  if (syncingTrackers.value) return;
  syncingTrackers.value = true;
  trackerSyncHint.value = "";
  try {
    const res = await syncPublicTrackers();
    settingsForm.btTrackers = [...res.btTrackers];
    invalidTrackers.value = [];
    trackerSyncHint.value =
      res.addedCount > 0
        ? `已同步最新公共列表，新增 ${res.addedCount} 个（共 ${res.totalCount} 个）`
        : `已是最新公共列表（共 ${res.totalCount} 个）`;
    updateFeedback(trackerSyncHint.value);
  } catch (error) {
    toastError(error, "同步公共 Tracker 失败");
  } finally {
    syncingTrackers.value = false;
  }
}

onMounted(() => {
  consumeCallbackQueryParams();
  void refreshCloud();
  window.addEventListener("focus", refreshCloud);
});

onUnmounted(() => {
  stopExternalPolling();
  window.removeEventListener("focus", refreshCloud);
});
</script>

<template>
  <SettingsSection id="download" title="下载">
    <input
      ref="jsonInputRef"
      type="file"
      accept=".json,application/json"
      class="hidden"
      @change="handleImportJsonAndAuthorize"
    />

    <div class="settings-group">
      <SettingsItem as="div" title="保存位置">
        <SegmentedControl v-model="storageMode" :options="STORAGE_MODE_OPTIONS" aria-label="保存位置" />
      </SettingsItem>

      <SettingsItem
        as="div"
        title="目录分组"
        desc="本机下载与云盘转存共用此文件夹层级（新任务生效）"
      >
        <AppSelect
          size="sm"
          class="w-[15rem]"
          :model-value="settingsForm.downloadFolderMode || 'anime_subgroup'"
          aria-label="目录分组"
          :options="[
            { value: 'anime', label: '按番剧名称（番剧名/）' },
            { value: 'anime_subgroup', label: '番剧 + 字幕组（番剧名/字幕组/）' },
            { value: 'subgroup', label: '按字幕组（字幕组/）' },
            { value: 'subgroup_anime', label: '字幕组 + 番剧（字幕组/番剧名/）' },
            { value: 'flat', label: '不分组（直接平铺）' }
          ]"
          @update:model-value="settingsForm.downloadFolderMode = String($event) as any"
        />
      </SettingsItem>

      <SettingsItem as="div" title="云盘服务">
        <SegmentedControl v-model="cloudProvider" :options="CLOUD_PROVIDER_OPTIONS" aria-label="云盘服务" />
      </SettingsItem>

      <!-- Google Drive 接入 -->
      <template v-if="cloudProvider === 'google'">
        <SettingsItem
          as="div"
          title="Google Drive"
          :desc="waitingExternalAuth ? '请在浏览器完成授权…' : undefined"
        >
          <div class="flex flex-wrap items-center justify-end gap-2">
            <template v-if="cloudConnected">
              <span class="status-pill status-pill-ok">已连接</span>
              <button type="button" class="settings-secondary-button" @click="openCloudDirectProbe">
                直连探测
              </button>
              <button
                type="button"
                class="settings-secondary-button"
                :disabled="authBusy"
                @click="startOAuth('google')"
              >
                重连
              </button>
              <button
                type="button"
                class="settings-secondary-button"
                :disabled="authBusy"
                @click="handleDisconnect"
              >
                断开
              </button>
            </template>

            <template v-else-if="hasGoogleCredentials">
              <button
                type="button"
                class="btn-primary inline-flex h-8 items-center gap-1.5 px-3.5 text-xs font-semibold"
                :disabled="authBusy"
                @click="startOAuth('google')"
              >
                <AppNavIcon name="external" class="size-3.5" aria-hidden="true" />
                <span>{{ authBusy ? "跳转中…" : "浏览器授权" }}</span>
              </button>
              <button
                type="button"
                class="settings-secondary-button"
                :disabled="authBusy"
                @click="jsonInputRef?.click()"
              >
                导入 JSON
              </button>
              <button type="button" class="settings-secondary-button" @click="openCloudDirectProbe">
                直连探测
              </button>
            </template>

            <template v-else>
              <button
                type="button"
                class="btn-primary inline-flex h-8 items-center gap-1.5 px-3.5 text-xs font-semibold"
                :disabled="authBusy"
                @click="jsonInputRef?.click()"
              >
                <AppNavIcon name="external" class="size-3.5" aria-hidden="true" />
                <span>导入 JSON 授权</span>
              </button>
              <button type="button" class="settings-secondary-button" @click="openCloudDirectProbe">
                直连探测
              </button>
            </template>
          </div>
        </SettingsItem>

        <ol v-if="!cloudConnected" class="oauth-guide">
          <li class="oauth-guide-row">
            <span class="oauth-step-num">1</span>
            <div class="oauth-step-body">
              <strong>新建项目</strong>
              <span>填 <kbd>项目名称</kbd> → 点 <kbd>创建</kbd></span>
            </div>
            <button
              type="button"
              class="settings-secondary-button oauth-open-btn"
              @click="openExternalLink('https://console.cloud.google.com/projectcreate')"
            >
              <span>打开</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </li>
          <li class="oauth-guide-row">
            <span class="oauth-step-num">2</span>
            <div class="oauth-step-body">
              <strong>启用 Drive API</strong>
              <span>点 <kbd>启用</kbd>（已启用显示 <kbd>管理</kbd>）</span>
            </div>
            <button
              type="button"
              class="settings-secondary-button oauth-open-btn"
              @click="openExternalLink('https://console.cloud.google.com/apis/library/drive.googleapis.com')"
            >
              <span>打开</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </li>
          <li class="oauth-guide-row">
            <span class="oauth-step-num">3</span>
            <div class="oauth-step-body">
              <strong>初始化 Auth 平台</strong>
              <span>
                点 <kbd>开始</kbd> → <kbd>应用名称</kbd> / <kbd>支持邮箱</kbd> → <kbd>外部</kbd> →
                <kbd>同意</kbd> → <kbd>创建</kbd>
              </span>
            </div>
            <button
              type="button"
              class="settings-secondary-button oauth-open-btn"
              @click="openExternalLink('https://console.cloud.google.com/auth/overview')"
            >
              <span>打开</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </li>
          <li class="oauth-guide-row">
            <span class="oauth-step-num">4</span>
            <div class="oauth-step-body">
              <strong>添加测试用户</strong>
              <span><kbd>测试用户</kbd> → <kbd>添加用户</kbd> → 输入 Gmail 回车 → <kbd>保存</kbd></span>
            </div>
            <button
              type="button"
              class="settings-secondary-button oauth-open-btn"
              @click="openExternalLink('https://console.cloud.google.com/auth/audience')"
            >
              <span>打开</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </li>
          <li class="oauth-guide-row">
            <span class="oauth-step-num">5</span>
            <div class="oauth-step-body">
              <strong>创建客户端</strong>
              <span>
                选 <kbd>桌面应用</kbd> → <kbd>创建</kbd> → <kbd>下载 JSON</kbd> → 回本页点
                <kbd>导入 JSON 授权</kbd>
              </span>
            </div>
            <button
              type="button"
              class="settings-secondary-button oauth-open-btn"
              @click="openExternalLink('https://console.cloud.google.com/auth/clients/create')"
            >
              <span>打开</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </li>
        </ol>

        <div v-if="!cloudConnected && !builtinCredentials" class="oauth-inline-box">
          <div class="grid gap-2.5 sm:grid-cols-2">
            <label class="grid min-w-0 gap-1 text-xs font-medium text-[var(--ink-secondary)]">
              <span>Client ID</span>
              <input
                v-model="settingsForm.googleClientId"
                class="settings-input settings-input-wide min-w-0"
                autocomplete="off"
                spellcheck="false"
                placeholder="xxx.apps.googleusercontent.com"
              />
            </label>
            <label class="grid min-w-0 gap-1 text-xs font-medium text-[var(--ink-secondary)]">
              <span>Client Secret</span>
              <input
                v-model="settingsForm.googleClientSecret"
                type="password"
                class="settings-input settings-input-wide min-w-0"
                autocomplete="off"
                placeholder="GOCSPX-..."
              />
            </label>
          </div>

          <div class="mt-2.5 flex flex-wrap items-center justify-between gap-2">
            <div
              v-if="!isLoopbackOrigin"
              class="flex flex-wrap items-center gap-1.5 text-[11.5px] text-[var(--muted)]"
            >
              <span>回调</span>
              <code>{{ defaultWebCallback }}</code>
              <button
                type="button"
                class="font-semibold text-[var(--accent-vivid)] hover:underline"
                @click="copyText(defaultWebCallback)"
              >
                复制
              </button>
            </div>
            <span v-else />

            <div class="flex items-center gap-2">
              <button
                type="button"
                class="settings-secondary-button"
                :disabled="authBusy"
                @click="jsonInputRef?.click()"
              >
                导入 JSON
              </button>
              <button
                type="button"
                class="btn-primary inline-flex h-8 items-center gap-1.5 px-3.5 text-xs font-semibold"
                :disabled="authBusy || !settingsForm.googleClientId.trim()"
                @click="saveManualGoogleAndAuthorize"
              >
                <AppNavIcon name="external" class="size-3.5" aria-hidden="true" />
                <span>保存并授权</span>
              </button>
            </div>
          </div>
        </div>
      </template>

      <!-- Microsoft OneDrive 接入 -->
      <template v-else-if="cloudProvider === 'onedrive'">
        <SettingsItem
          as="div"
          title="Microsoft OneDrive"
          :desc="waitingExternalAuth ? '请在浏览器完成授权…' : undefined"
        >
          <div class="flex flex-wrap items-center justify-end gap-2">
            <template v-if="cloudConnected">
              <span class="status-pill status-pill-ok">已连接</span>
              <button type="button" class="settings-secondary-button" @click="openCloudDirectProbe">
                直连探测
              </button>
              <button
                type="button"
                class="settings-secondary-button"
                :disabled="authBusy"
                @click="startOAuth('onedrive')"
              >
                重连
              </button>
              <button
                type="button"
                class="settings-secondary-button"
                :disabled="authBusy"
                @click="handleDisconnect"
              >
                断开
              </button>
            </template>

            <template v-else-if="hasOneDriveCredentials">
              <button
                type="button"
                class="btn-primary inline-flex h-8 items-center gap-1.5 px-3.5 text-xs font-semibold"
                :disabled="authBusy"
                @click="startOAuth('onedrive')"
              >
                <AppNavIcon name="external" class="size-3.5" aria-hidden="true" />
                <span>{{ authBusy ? "跳转中…" : "浏览器授权" }}</span>
              </button>
              <button type="button" class="settings-secondary-button" @click="openCloudDirectProbe">
                直连探测
              </button>
            </template>

            <template v-else>
              <button type="button" class="settings-secondary-button" @click="openCloudDirectProbe">
                直连探测
              </button>
            </template>
          </div>
        </SettingsItem>

        <ol v-if="!cloudConnected" class="oauth-guide">
          <li class="oauth-guide-row">
            <span class="oauth-step-num">1</span>
            <div class="oauth-step-body">
              <strong>注册应用</strong>
              <span>
                受支持账户选 <kbd>任何组织目录与个人 Microsoft 账户</kbd> → 重定向选
                <kbd>Web</kbd> 填入 <code>{{ defaultWebCallback }}</code>
              </span>
            </div>
            <button
              type="button"
              class="settings-secondary-button oauth-open-btn"
              @click="openExternalLink('https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/CreateApplicationBlade')"
            >
              <span>打开</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </li>
          <li class="oauth-guide-row">
            <span class="oauth-step-num">2</span>
            <div class="oauth-step-body">
              <strong>复制客户端 ID</strong>
              <span>概览页复制 <kbd>应用程序(客户端) ID</kbd>；<kbd>证书和密码</kbd> 新建密码（公共客户端可留空）</span>
            </div>
            <button
              type="button"
              class="settings-secondary-button oauth-open-btn"
              @click="openExternalLink('https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade')"
            >
              <span>打开</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </li>
        </ol>

        <div v-if="!cloudConnected && !builtinCredentials" class="oauth-inline-box">
          <div class="grid gap-2.5 sm:grid-cols-2">
            <label class="grid min-w-0 gap-1 text-xs font-medium text-[var(--ink-secondary)]">
              <span>Application (Client) ID</span>
              <input
                v-model="settingsForm.onedriveClientId"
                class="settings-input settings-input-wide min-w-0"
                autocomplete="off"
                spellcheck="false"
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              />
            </label>
            <label class="grid min-w-0 gap-1 text-xs font-medium text-[var(--ink-secondary)]">
              <span>Client Secret（选填）</span>
              <input
                v-model="settingsForm.onedriveClientSecret"
                type="password"
                class="settings-input settings-input-wide min-w-0"
                autocomplete="off"
                placeholder="公共客户端留空，Web 应用填写"
              />
            </label>
          </div>

          <div class="mt-2.5 flex flex-wrap items-center justify-between gap-2">
            <div class="flex flex-wrap items-center gap-1.5 text-[11.5px] text-[var(--muted)]">
              <span>回调</span>
              <code>{{ defaultWebCallback }}</code>
              <button
                type="button"
                class="font-semibold text-[var(--accent-vivid)] hover:underline"
                @click="copyText(defaultWebCallback)"
              >
                复制
              </button>
            </div>

            <button
              type="button"
              class="btn-primary inline-flex h-8 items-center gap-1.5 px-3.5 text-xs font-semibold"
              :disabled="authBusy || !settingsForm.onedriveClientId.trim()"
              @click="saveManualOneDriveAndAuthorize"
            >
              <AppNavIcon name="external" class="size-3.5" aria-hidden="true" />
              <span>保存并授权</span>
            </button>
          </div>
        </div>
      </template>

      <!-- WebDAV 接入（OpenList / Alist / 坚果云 / NAS） -->
      <template v-else>
        <SettingsItem stacked title="WebDAV 地址">
          <input
            v-model="settingsForm.webdavUrl"
            class="settings-input settings-input-wide"
            spellcheck="false"
            placeholder="https://dav.jianguoyun.com/dav/ 或 http://127.0.0.1:5244/dav"
          />
        </SettingsItem>

        <SettingsItem title="账号密码">
          <span class="settings-pair">
            <input
              v-model="settingsForm.webdavUsername"
              class="settings-input"
              autocomplete="off"
              placeholder="用户名"
              aria-label="WebDAV 用户名"
            />
            <input
              v-model="settingsForm.webdavPassword"
              type="text"
              class="secret-mask-input settings-input"
              autocomplete="off"
              placeholder="密码 / 应用授权码"
              aria-label="WebDAV 密码"
            />
          </span>
        </SettingsItem>

        <SettingsItem as="div" title="WebDAV 状态">
          <div class="flex flex-wrap items-center justify-end gap-2">
            <span v-if="cloudConnected" class="status-pill status-pill-ok">已连接</span>
            <button
              type="button"
              :class="cloudConnected ? 'settings-secondary-button' : 'btn-primary inline-flex h-8 items-center px-3.5 text-xs font-semibold'"
              :disabled="authBusy || !settingsForm.webdavUrl.trim()"
              @click="handleConnectWebDAV"
            >
              {{ authBusy ? "验证中…" : cloudConnected ? "重新验证" : "连接" }}
            </button>
            <button
              v-if="cloudConnected"
              type="button"
              class="settings-secondary-button"
              :disabled="authBusy"
              @click="handleDisconnect"
            >
              断开
            </button>
          </div>
        </SettingsItem>
      </template>

      <div v-if="oauthError" class="oauth-alert" role="alert">
        <p class="min-w-0 flex-1 text-xs text-[var(--err)]">{{ oauthError }}</p>
        <button type="button" class="settings-secondary-button shrink-0" @click="oauthError = ''">关闭</button>
      </div>

      <!-- 统一云盘归档目录 -->
      <SettingsItem v-if="cloudConnected || storageMode !== 'local'" as="div" title="归档目录">
        <div class="flex flex-wrap items-center gap-2">
          <button
            v-if="cloudConnected"
            type="button"
            class="settings-secondary-button inline-flex items-center gap-1.5"
            :disabled="folderBusy"
            @click="toggleFolderPicker"
          >
            <AppNavIcon name="folder" class="size-3.5 text-[var(--muted)]" aria-hidden="true" />
            <span>{{ currentFolderDisplay }}</span>
          </button>
          <input
            v-else
            v-model="activeFolderValue"
            class="settings-input w-44 text-xs"
            :placeholder="cloudProvider === 'webdav' ? 'Bangumi Cat' : 'root'"
            aria-label="云盘归档目录"
          />
        </div>
      </SettingsItem>

      <div v-if="cloudConnected && folderPickerOpen" class="oauth-inline-box">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="text-xs font-semibold text-[var(--ink)]">
            {{ folderTrail.map((f) => f.name).join(" / ") }}
          </span>
          <div class="flex flex-wrap gap-1.5">
            <button
              v-if="folderTrail.length > 1"
              type="button"
              class="settings-secondary-button"
              :disabled="folderBusy"
              @click="stepUpPickerFolder"
            >
              上一级
            </button>
            <button
              type="button"
              class="settings-secondary-button"
              :disabled="folderBusy"
              @click="createAndApplyBangumiFolder"
            >
              新建 Bangumi Cat
            </button>
            <button
              type="button"
              class="btn-primary px-3 py-1 text-xs font-semibold"
              :disabled="folderBusy"
              @click="applyArchiveFolder(currentPickerFolder)"
            >
              选此目录
            </button>
          </div>
        </div>

        <ul v-if="folderList.length" class="folder-picker-list mt-2.5">
          <li v-for="folder in folderList" :key="folder.id">
            <button type="button" :disabled="folderBusy" @click="enterPickerFolder(folder)">
              <span>📁 {{ folder.name }}</span>
              <span class="text-xs text-[var(--muted)]">→</span>
            </button>
          </li>
        </ul>
        <p v-else class="mt-2 text-xs text-[var(--muted)]">
          {{ folderBusy ? "读取中…" : "无子文件夹" }}
        </p>
        <button
          v-if="folderNextToken"
          type="button"
          class="settings-secondary-button mt-2"
          :disabled="folderBusy"
          @click="loadPickerFolders(true)"
        >
          更多
        </button>
      </div>

      <!-- 通用下载引擎 -->
      <SettingsItem as="div" title="下载引擎">
        <SegmentedControl v-model="downloadClient" :options="DOWNLOAD_CLIENT_OPTIONS" aria-label="下载引擎" />
      </SettingsItem>

      <template v-if="settingsForm.downloadClient === 'qbittorrent'">
        <SettingsItem stacked title="Web UI 地址">
          <input
            v-model="settingsForm.qbittorrentBaseUrl"
            class="settings-input settings-input-wide"
            spellcheck="false"
            placeholder="http://127.0.0.1:8080"
          />
        </SettingsItem>

        <SettingsItem title="账号密码">
          <span class="settings-pair">
            <input
              v-model="settingsForm.qbittorrentUsername"
              class="settings-input"
              autocomplete="off"
              placeholder="用户名"
              aria-label="qBittorrent 用户名"
            />
            <input
              v-model="settingsForm.qbittorrentPassword"
              type="text"
              class="secret-mask-input settings-input"
              autocomplete="off"
              placeholder="密码"
              aria-label="qBittorrent 密码"
            />
          </span>
        </SettingsItem>
      </template>

      <template v-else-if="settingsForm.downloadClient === 'aria2-rpc'">
        <SettingsItem stacked title="RPC 地址">
          <input
            v-model="settingsForm.aria2RpcUrl"
            class="settings-input settings-input-wide"
            spellcheck="false"
            placeholder="http://127.0.0.1:6800/jsonrpc"
          />
        </SettingsItem>

        <SettingsItem title="RPC 密钥">
          <input
            v-model="settingsForm.aria2RpcSecret"
            type="text"
            class="secret-mask-input settings-input"
            autocomplete="off"
            placeholder="选填（rpc-secret）"
            aria-label="Aria2 RPC 密钥"
          />
        </SettingsItem>
      </template>

      <template v-else-if="settingsForm.downloadClient === 'transmission'">
        <SettingsItem stacked title="RPC 地址">
          <input
            v-model="settingsForm.transmissionRpcUrl"
            class="settings-input settings-input-wide"
            spellcheck="false"
            placeholder="http://127.0.0.1:9091/transmission/rpc"
          />
        </SettingsItem>

        <SettingsItem title="账号密码">
          <span class="settings-pair">
            <input
              v-model="settingsForm.transmissionUsername"
              class="settings-input"
              autocomplete="off"
              placeholder="用户名（选填）"
              aria-label="Transmission 用户名"
            />
            <input
              v-model="settingsForm.transmissionPassword"
              type="text"
              class="secret-mask-input settings-input"
              autocomplete="off"
              placeholder="密码（选填）"
              aria-label="Transmission 密码"
            />
          </span>
        </SettingsItem>
      </template>

      <SettingsItem
        v-if="settingsForm.downloadClient !== 'native'"
        as="div"
        title="引擎连接"
        :desc="
          engineTestResult
            ? engineTestResult.ok
              ? `${engineTestResult.version || '连接正常'} · ${engineTestResult.latencyMs} ms`
              : engineTestResult.error
            : undefined
        "
      >
        <div class="flex items-center gap-2">
          <span
            v-if="engineTestResult"
            class="status-pill"
            :class="engineTestResult.ok ? 'status-pill-ok' : 'status-pill-err'"
          >
            {{ engineTestResult.ok ? "在线" : "离线" }}
          </span>
          <button
            type="button"
            class="settings-secondary-button"
            :disabled="engineTesting"
            @click="handleTestEngine"
          >
            {{ engineTesting ? "测试中…" : "测试连接" }}
          </button>
        </div>
      </SettingsItem>
      <SettingsItem
        v-if="settingsForm.downloadClient === 'native' || settingsForm.downloadClient === 'aria2-rpc'"
        as="div"
        title="下载完成后继续做种"
        desc="为已完成且保留在本机的 BT 番剧持续上传做种，有助于提升个人分享率与冷门番存活率（若开启“仅云盘”删本地文件则自动跳过）"
      >
        <label class="inline-flex cursor-pointer items-center gap-2 text-xs font-semibold text-[var(--ink)]">
          <input
            v-model="settingsForm.aria2SeedEnabled"
            type="checkbox"
            class="size-4 accent-[var(--accent)]"
          />
          <span>{{ settingsForm.aria2SeedEnabled ? "已开启" : "已关闭" }}</span>
        </label>
      </SettingsItem>

      <SettingsItem
        v-if="settingsForm.downloadClient === 'native' || settingsForm.downloadClient === 'aria2-rpc'"
        as="div"
        stacked
        title="额外 Tracker"
        desc="启动时会自动拉取 XIU2 / ngosang 公共 Tracker 订阅源；也可点下方按钮立即同步最新列表或手动补充"
      >
        <div class="grid w-full min-w-0 gap-2">
          <textarea
            v-model="trackerDraft"
            aria-label="公共 Tracker 列表"
            class="settings-input settings-input-wide font-mono text-xs"
            rows="4"
            spellcheck="false"
            placeholder="已内置公共 Tracker 并自动同步在线订阅，可按行补充自定义地址"
            @blur="commitTrackers"
            @keydown.enter.stop
          />
          <div class="settings-actions">
            <button
              type="button"
              class="settings-secondary-button"
              :disabled="syncingTrackers"
              @click="handleSyncPublicTrackers"
            >
              {{ syncingTrackers ? "同步中…" : "一键同步最新公共 Tracker" }}
            </button>
            <button type="button" class="settings-secondary-button" @click="fillRecommendedTrackers">填入精选</button>
            <span v-if="settingsForm.btTrackers.length" class="settings-desc">已附加 {{ settingsForm.btTrackers.length }} 个</span>
          </div>
          <p v-if="trackerSyncHint" class="settings-desc text-[var(--ok)]">
            {{ trackerSyncHint }}
          </p>
          <p v-if="invalidTrackers.length" class="settings-field-error">
            无效地址：{{ invalidTrackers.join("、") }}
          </p>
        </div>
      </SettingsItem>

      <SettingsItem stacked title="ffmpeg 路径">
        <input v-model="settingsForm.ffmpegPath" class="settings-input settings-input-wide" placeholder="留空自动查找：桌面版内置，其次系统安装的 ffmpeg" />
      </SettingsItem>
    </div>
  </SettingsSection>
</template>

<style scoped>
.oauth-alert {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0.75rem 0.875rem;
  background: color-mix(in srgb, var(--err-soft) 55%, transparent);
}

.oauth-inline-box {
  padding: 0.75rem 0.875rem;
  background: color-mix(in srgb, var(--panel) 55%, transparent);
}

.oauth-guide {
  margin: 0;
  padding: 0;
  list-style: none;
  background: color-mix(in srgb, var(--panel) 55%, transparent);
}

.oauth-guide-row {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 0.55rem 0.875rem;
}

.oauth-guide-row + .oauth-guide-row {
  border-top: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
}

.oauth-step-num {
  display: inline-flex;
  height: 1.25rem;
  width: 1.25rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: var(--tint-2);
  font-size: 0.6875rem;
  font-weight: 700;
  color: var(--ink-secondary);
}

.oauth-step-body {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.375rem;
  font-size: 0.78125rem;
  line-height: 1.5;
  color: var(--muted);
}

.oauth-step-body strong {
  color: var(--ink);
  font-weight: 600;
}

.oauth-step-body kbd {
  display: inline-block;
  padding: 0.05rem 0.35rem;
  border-radius: 0.275rem;
  border: 1px solid var(--line);
  background: var(--card);
  font-family: inherit;
  font-size: 0.725rem;
  font-weight: 500;
  line-height: 1.35;
  color: var(--ink);
}

.oauth-open-btn {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 0.25rem;
}

.folder-picker-list {
  max-height: 196px;
  overflow: auto;
  border-radius: 0.5rem;
  border: 1px solid var(--line);
  background: var(--card);
}

.folder-picker-list li + li {
  border-top: 1px solid var(--line);
}

.folder-picker-list button {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 10px;
  font-size: 0.8125rem;
  color: var(--ink);
  text-align: left;
}

.folder-picker-list button:hover:not(:disabled) {
  background: var(--tint-2);
}
</style>
