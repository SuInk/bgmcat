<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import type { PlaybackSubtitleTrack } from "@shared/index";
import {
  connectCloud,
  fetchFileTextPreview,
  getCloudFileContentUrl,
  getDownloadAssSubtitleUrl,
  getDownloadHlsPlaylistUrl,
  getDownloadStreamUrl,
  getDownloadSubtitleUrl,
  getLocalFileContentUrl,
  type FileBrowserEntry,
  type FileEntryKind
} from "../api";
import AppDialog from "../components/common/AppDialog.vue";
import ArtPlayerPlaceholder from "../components/downloads/ArtPlayerPlaceholder.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { copyText, openExternalLink } from "../lib/actions";
import { formatBytes, formatDateTime } from "../lib/format";
import type { PlaybackHistoryMeta } from "../lib/playbackHistory";
import { selectTab } from "../stores/boot";
import { isBusy } from "../stores/busy";
import {
  activeFilePlayback,
  cloudArchiveFolderId,
  cloudConnected,
  cloudConfigured,
  cloudCurrentFolder,
  cloudEntries,
  cloudError,
  cloudLoaded,
  cloudLoading,
  cloudLoadingMore,
  cloudNextPageToken,
  cloudProviderName,
  cloudQuota,
  cloudTrail,
  closeFilePlayback,
  createSubfolderInCurrent,
  emptyLocalTrashEntries,
  fileSource,
  loadCloudDirectory,
  loadLocalDirectory,
  loadMoreCloudFiles,
  localEntries,
  localError,
  localFreeBytes,
  localLoaded,
  localLoading,
  localPath,
  localRootPath,
  localTotalBytes,
  localTrashBytes,
  localTrashCount,
  openFilePlayback,
  refreshActiveFiles,
  removeFileBrowserEntry,
  restoreLocalTrashEntry,
  setFileSource,
  type FileStorageSource
} from "../stores/files";
import { requestedSettingsPage } from "../stores/router";
import {
  SHARE_FEATURE_ENABLED,
  activeSharesCount,
  openShareForFileEntry,
  openShareManager,
  refreshShares
} from "../stores/shares";
import { isDesktopShell, settingsForm } from "../stores/session";
import { toastError, updateFeedback } from "../stores/toasts";

const ArtPlayerPane = defineAsyncComponent({
  loader: () => import("../components/downloads/ArtPlayerPane.vue"),
  loadingComponent: ArtPlayerPlaceholder,
  delay: 0
});

type FileSortField = "name" | "size" | "time";
type FilePreviewKind = "image" | "video" | "audio" | "pdf" | "text" | "other";

interface FilePreviewState {
  entry: FileBrowserEntry;
  source: FileStorageSource;
  kind: FilePreviewKind;
  url: string;
  downloadUrl: string;
  loading: boolean;
  error: string;
  text: string;
  clipped: boolean;
}

const TEXT_PREVIEW_LIMIT = 256 * 1024;

const filterQuery = ref("");
const sortField = ref<FileSortField>("name");
const creatingFolder = ref(false);
const newFolderName = ref("");
const newFolderInputRef = ref<HTMLInputElement | null>(null);
const pendingDelete = ref<FileBrowserEntry | null>(null);
const confirmingEmptyTrash = ref(false);
const preview = shallowRef<FilePreviewState | null>(null);
let previewAbort: AbortController | null = null;
let previewSeq = 0;

function ensureActiveSourceLoaded(source: FileStorageSource): void {
  if (source === "local" && !localLoaded.value && !localLoading.value) {
    void loadLocalDirectory();
  } else if (source === "cloud" && !cloudLoaded.value && !cloudLoading.value) {
    void loadCloudDirectory();
  }
}

onMounted(() => {
  const params = new URLSearchParams(window.location.search);
  if (params.has("cloud") || params.has("cloudError")) {
    const cloudErr = params.get("cloudError");
    const connected = params.get("cloud") === "connected";
    params.delete("cloud");
    params.delete("cloudError");
    const nextSearch = params.toString();
    window.history.replaceState(null, "", `${window.location.pathname}${nextSearch ? `?${nextSearch}` : ""}${window.location.hash}`);
    setFileSource("cloud");
    if (cloudErr) {
      updateFeedback(`云盘授权未完成：${cloudErr}`, "error");
    } else if (connected) {
      updateFeedback("云盘授权成功", "success");
      void loadCloudDirectory({ resetTrail: true });
    }
  }
  void refreshShares(true);
  ensureActiveSourceLoaded(fileSource.value);
  // 顺手在后台探一下另一侧容量/连接态，让顶栏页签副信息立即可见
  if (fileSource.value === "local" && !cloudLoaded.value) {
    void loadCloudDirectory();
  } else if (fileSource.value === "cloud" && !localLoaded.value) {
    void loadLocalDirectory();
  }
});

onUnmounted(() => {
  previewAbort?.abort();
});

watch(fileSource, (next) => {
  pendingDelete.value = null;
  confirmingEmptyTrash.value = false;
  closePreview();
  creatingFolder.value = false;
  filterQuery.value = "";
  ensureActiveSourceLoaded(next);
});

watch(
  () => settingsForm.cloudProvider,
  () => {
    closePreview();
    if (cloudLoaded.value) {
      void loadCloudDirectory({ resetTrail: true });
    }
  }
);

function switchSource(next: FileStorageSource): void {
  if (fileSource.value === next) {
    return;
  }
  setFileSource(next);
}

const localFolderLabels = ref<Record<string, string>>({});

function entryLabel(entry: FileBrowserEntry): string {
  return entry.displayName || entry.name;
}

function entryTooltip(entry: FileBrowserEntry): string {
  if (entry.displayName && entry.displayName !== entry.name) {
    return `${entry.displayName}\n原始名称：${entry.name}`;
  }
  return entry.name;
}

/* ---------- 面包屑与上级导航 ---------- */
const inLocalTrash = computed(
  () => fileSource.value === "local" && (localPath.value === ".trash" || localPath.value.startsWith(".trash/"))
);

const localCrumbs = computed(() => {
  const base = [{ label: "下载目录", path: "" }];
  if (!localPath.value) {
    return base;
  }
  const segments = localPath.value.split("/").filter(Boolean);
  let acc = "";
  for (const seg of segments) {
    acc = acc ? `${acc}/${seg}` : seg;
    const label = acc === ".trash" ? "回收站" : localFolderLabels.value[acc] || seg;
    base.push({ label, path: acc });
  }
  return base;
});

function toggleLocalTrashView(): void {
  pendingDelete.value = null;
  creatingFolder.value = false;
  filterQuery.value = "";
  void loadLocalDirectory(inLocalTrash.value ? "" : ".trash");
}

const canGoUp = computed(() =>
  fileSource.value === "local" ? Boolean(localPath.value) : cloudTrail.value.length > 1
);

function goUp(): void {
  if (!canGoUp.value) {
    return;
  }
  pendingDelete.value = null;
  if (fileSource.value === "local") {
    const parts = localPath.value.split("/").filter(Boolean);
    parts.pop();
    void loadLocalDirectory(parts.join("/"));
  } else if (cloudTrail.value.length > 1) {
    const target = cloudTrail.value[cloudTrail.value.length - 2]!;
    void loadCloudDirectory({ parent: target.id, parentName: target.name });
  }
}

function jumpLocalCrumb(path: string): void {
  if (path === localPath.value && localLoaded.value) {
    return;
  }
  pendingDelete.value = null;
  void loadLocalDirectory(path);
}

function jumpCloudCrumb(folder: { id: string; name: string }, index: number): void {
  if (index === cloudTrail.value.length - 1 && cloudLoaded.value) {
    return;
  }
  pendingDelete.value = null;
  cloudTrail.value = cloudTrail.value.slice(0, index + 1);
  void loadCloudDirectory({ parent: folder.id, parentName: folder.name });
}

const isViewingCloudRoot = computed(
  () => cloudTrail.value[0]?.id === "root" && cloudArchiveFolderId.value !== "root"
);

function switchCloudRootMode(mode: "archive" | "root"): void {
  pendingDelete.value = null;
  if (mode === "root") {
    void loadCloudDirectory({ parent: "root", parentName: "我的云盘", resetTrail: true });
  } else {
    void loadCloudDirectory({ parent: "default", resetTrail: true });
  }
}

/* ---------- 列表筛选与排序 ---------- */
const currentLoading = computed(() =>
  fileSource.value === "local" ? localLoading.value : cloudLoading.value
);

const currentError = computed(() =>
  fileSource.value === "local" ? localError.value : cloudError.value
);

const rawEntries = computed(() =>
  fileSource.value === "local" ? localEntries.value : cloudEntries.value
);

const visibleEntries = computed(() => {
  const kw = filterQuery.value.trim().toLowerCase();
  const filtered = kw
    ? rawEntries.value.filter(
        (item) =>
          item.name.toLowerCase().includes(kw) ||
          Boolean(item.displayName && item.displayName.toLowerCase().includes(kw))
      )
    : rawEntries.value;

  return [...filtered].sort((a, b) => {
    if (a.isDir !== b.isDir) {
      return a.isDir ? -1 : 1;
    }
    if (sortField.value === "size" && !a.isDir && !b.isDir && a.size !== b.size) {
      return b.size - a.size;
    }
    if (sortField.value === "time" && a.modTime !== b.modTime) {
      return b.modTime.localeCompare(a.modTime);
    }
    return entryLabel(a).localeCompare(entryLabel(b), "zh-CN", { numeric: true, sensitivity: "base" });
  });
});

const summaryStats = computed(() => {
  let folders = 0;
  let files = 0;
  let bytes = 0;
  for (const item of visibleEntries.value) {
    if (item.isDir) {
      folders += 1;
      bytes += item.size;
    } else {
      files += 1;
      bytes += item.size;
    }
  }
  const parts: string[] = [];
  if (folders > 0) parts.push(`${folders} 文件夹`);
  parts.push(`${files} 文件`);
  if (bytes > 0) parts.push(formatBytes(bytes));
  return parts.join(" · ");
});

const localCapacityBadge = computed(() => {
  if (!localFreeBytes.value) {
    return "";
  }
  return `剩余 ${formatBytes(localFreeBytes.value)}`;
});

const cloudCapacityBadge = computed(() => {
  if (cloudConnected.value === false) {
    return "未连接";
  }
  if (cloudQuota.value && cloudQuota.value.limitBytes > 0) {
    return `${formatBytes(cloudQuota.value.usageBytes)} / ${formatBytes(cloudQuota.value.limitBytes)}`;
  }
  if (cloudQuota.value && cloudQuota.value.usageBytes > 0) {
    return `已用 ${formatBytes(cloudQuota.value.usageBytes)}`;
  }
  return cloudConnected.value ? "已连接" : "";
});

/* ---------- 条目交互、预览与删除 ---------- */
function iconForKind(kind: FileEntryKind): string {
  switch (kind) {
    case "folder":
      return "folder";
    case "video":
      return "file-video";
    case "audio":
      return "file-audio";
    case "image":
      return "file-image";
    case "pdf":
    case "subtitle":
    case "text":
      return "file-text";
    case "archive":
    case "torrent":
      return "file-archive";
    default:
      return "file";
  }
}

function kindBadgeLabel(entry: FileBrowserEntry): string {
  if (entry.isDir) {
    return "文件夹";
  }
  if (entry.ext) {
    return entry.ext.toUpperCase();
  }
  const mime = (entry.mimeType || "").toLowerCase();
  if (mime.includes("google-apps.document")) return "DOC";
  if (mime.includes("google-apps.spreadsheet")) return "SHEET";
  if (mime.includes("google-apps.presentation")) return "SLIDE";
  switch (entry.kind) {
    case "video":
      return "VIDEO";
    case "audio":
      return "AUDIO";
    case "image":
      return "IMG";
    case "pdf":
      return "PDF";
    case "text":
      return "TXT";
    case "subtitle":
      return "SUB";
    default:
      return "FILE";
  }
}

function entryContentUrl(
  entry: FileBrowserEntry,
  download = false,
  source: FileStorageSource = fileSource.value
): string {
  if (source === "local") {
    return getLocalFileContentUrl(entry.path, download);
  }
  return getCloudFileContentUrl(entry.id, download, settingsForm.cloudProvider, {
    name: entry.name,
    mimeType: entry.mimeType
  });
}

function resolveInitialPreviewKind(entry: FileBrowserEntry): FilePreviewKind {
  switch (entry.kind) {
    case "image":
      return "image";
    case "pdf":
      return "pdf";
    case "video":
      return "video";
    case "audio":
      return "audio";
    case "archive":
    case "torrent":
      return "other";
    case "subtitle":
    case "text":
    default:
      // 未知或无扩展名文件（如 Google Docs 或无后缀备注）统一尝试读取前 256 KB 嗅探纯文本
      return "text";
  }
}

function closePreview(): void {
  previewSeq += 1;
  previewAbort?.abort();
  previewAbort = null;
  preview.value = null;
}

async function openPreview(entry: FileBrowserEntry, source: FileStorageSource = fileSource.value): Promise<void> {
  if (entry.isDir) {
    return;
  }
  const seq = ++previewSeq;
  previewAbort?.abort();
  const controller = new AbortController();
  previewAbort = controller;

  const kind = resolveInitialPreviewKind(entry);
  const url = entryContentUrl(entry, false, source);
  const downloadUrl = entryContentUrl(entry, true, source);
  const state: FilePreviewState = {
    entry,
    source,
    kind,
    url,
    downloadUrl,
    loading: kind === "text",
    error: "",
    text: "",
    clipped: false
  };
  preview.value = state;

  if (kind !== "text") {
    return;
  }
  try {
    const res = await fetchFileTextPreview(url, TEXT_PREVIEW_LIMIT, controller.signal);
    if (seq !== previewSeq || !preview.value) return;
    if (!res.supported) {
      preview.value = { ...state, kind: "other", loading: false };
      return;
    }
    preview.value = {
      ...state,
      loading: false,
      text: res.text,
      clipped: res.clipped
    };
  } catch (err) {
    if (controller.signal.aborted || seq !== previewSeq || !preview.value) return;
    preview.value = {
      ...state,
      loading: false,
      error: err instanceof Error ? err.message : "预览读取失败"
    };
  }
}

async function copyPreviewText(): Promise<void> {
  if (!preview.value?.text) return;
  await copyText(preview.value.text);
  updateFeedback("已复制文本内容", "success");
}

const previewMetaLine = computed(() => {
  const cur = preview.value;
  if (!cur) return "";
  const parts: string[] = [kindBadgeLabel(cur.entry)];
  if (cur.entry.size > 0) {
    parts.push(formatBytes(cur.entry.size));
  }
  const dt = formatDateTime(cur.entry.modTime, "");
  if (dt) {
    parts.push(dt);
  }
  return parts.join(" · ");
});

function handleEntryActivate(entry: FileBrowserEntry): void {
  if (entry.isDir) {
    pendingDelete.value = null;
    filterQuery.value = "";
    if (fileSource.value === "local") {
      if (entry.displayName) {
        localFolderLabels.value[entry.path] = entry.displayName;
      }
      void loadLocalDirectory(entry.path);
    } else {
      void loadCloudDirectory({ parent: entry.id, parentName: entryLabel(entry) });
    }
    return;
  }
  if (entry.playable && !inLocalTrash.value) {
    void openFilePlayback(entry, { entries: visibleEntries.value });
    return;
  }
  void openPreview(entry);
}

async function openCreateFolderBar(): Promise<void> {
  creatingFolder.value = true;
  newFolderName.value = "";
  await nextTick();
  newFolderInputRef.value?.focus();
}

async function submitNewFolder(): Promise<void> {
  const ok = await createSubfolderInCurrent(newFolderName.value);
  if (ok) {
    creatingFolder.value = false;
    newFolderName.value = "";
  }
}

function askDelete(entry: FileBrowserEntry): void {
  pendingDelete.value = entry;
}

const deleteWillTrash = computed(() => {
  if (!pendingDelete.value) return false;
  if (fileSource.value === "local") {
    return !inLocalTrash.value;
  }
  return settingsForm.cloudProvider !== "webdav";
});

async function runConfirmedDelete(permanent = false): Promise<void> {
  const target = pendingDelete.value;
  if (!target) return;
  const ok = await removeFileBrowserEntry(target, { permanent: permanent || inLocalTrash.value });
  if (ok) {
    if (preview.value?.entry.id === target.id) {
      closePreview();
    }
    pendingDelete.value = null;
  }
}

async function runEmptyTrash(): Promise<void> {
  const ok = await emptyLocalTrashEntries();
  if (ok) {
    confirmingEmptyTrash.value = false;
  }
}

function openStorageSettings(target: "data" | "download"): void {
  requestedSettingsPage.value = target;
  selectTab("settings");
}

async function authorizeCloudFromFiles(): Promise<void> {
  if (!cloudConfigured.value || settingsForm.cloudProvider === "webdav") {
    openStorageSettings("download");
    return;
  }
  try {
    if (isDesktopShell) {
      const res = await connectCloud({ provider: settingsForm.cloudProvider });
      if (res.url) {
        openExternalLink(res.url);
      }
      updateFeedback("已在系统浏览器打开授权页", "info");
      return;
    }
    const res = await connectCloud({
      provider: settingsForm.cloudProvider,
      returnTo: `/files?cloud=connected&provider=${settingsForm.cloudProvider}`
    });
    if (res.url) {
      window.location.assign(res.url);
    }
  } catch (error) {
    toastError(error, "发起授权失败");
  }
}

async function copyLocalRootPath(): Promise<void> {
  if (!localRootPath.value) return;
  await copyText(localRootPath.value);
  updateFeedback("已复制本地下载目录路径", "success");
}

/* ---------- 播放器辅助 ---------- */
const playbackStreamUrl = computed(() => {
  const pb = activeFilePlayback.value?.info;
  if (!pb) return "";
  return pb.deliveryMode === "direct"
    ? getDownloadStreamUrl(pb.gid)
    : pb.deliveryMode === "hls-remux" || pb.deliveryMode === "hls-transcode"
      ? getDownloadHlsPlaylistUrl(pb.gid)
      : "";
});

function playbackSubtitleUrl(track: PlaybackSubtitleTrack): string {
  const gid = activeFilePlayback.value?.info.gid ?? "";
  const codec = track.codec?.toLowerCase() ?? "";
  return codec === "ass" || codec === "ssa"
    ? getDownloadAssSubtitleUrl(gid, track.id)
    : getDownloadSubtitleUrl(gid, track.id);
}

const playbackMetaLine = computed(() => {
  const state = activeFilePlayback.value;
  if (!state) return "";
  return [state.folderLabel, formatBytes(state.entry.size)].filter(Boolean).join(" · ");
});

const playbackHistoryMeta = computed<PlaybackHistoryMeta | undefined>(() => {
  const state = activeFilePlayback.value;
  if (!state) return undefined;
  const entry = state.entry;
  return {
    kind: "file",
    title: entryLabel(entry),
    folderLabel: state.folderLabel,
    fileSource: state.source,
    fileEntry: {
      id: entry.id,
      name: entry.name,
      displayName: entry.displayName,
      path: entry.path,
      kind: entry.kind,
      size: entry.size,
      modTime: entry.modTime,
      ext: entry.ext,
      mediaId: entry.mediaId || state.info.gid
    }
  };
});

function playNextSibling(): void {
  const state = activeFilePlayback.value;
  if (!state || state.siblings.length <= 1) return;
  const idx = state.siblings.findIndex((item) => item.id === state.entry.id);
  const next = idx >= 0 ? state.siblings[idx + 1] : undefined;
  if (next) {
    void openFilePlayback(next, {
      source: state.source,
      entries: state.siblings,
      folderLabel: state.folderLabel
    });
  }
}

async function copyActiveStreamLink(): Promise<void> {
  const state = activeFilePlayback.value;
  if (!state) return;
  const raw = new URL(getDownloadStreamUrl(state.info.gid), window.location.origin).toString();
  await copyText(raw);
  updateFeedback("已复制带鉴权的媒体流地址，可粘贴到 IINA / VLC / PotPlayer 打开", "success");
}
</script>

<template>
  <!-- 在线点播态（本地与 Google Drive 云端共用 ArtPlayer + 软字幕 + 同目录选集） -->
  <div v-if="activeFilePlayback" class="files-player motion-rise">
    <header class="player-head">
      <button
        type="button"
        class="ctrl-icon-btn"
        aria-label="返回文件列表"
        title="返回文件列表"
        @click="closeFilePlayback"
      >
        <AppNavIcon name="chevron-left" class="size-4" />
      </button>
      <div class="min-w-0 flex-1">
        <h2 class="truncate text-[14.5px] font-semibold leading-snug text-[var(--ink)]" :title="entryTooltip(activeFilePlayback.entry)">
          {{ entryLabel(activeFilePlayback.entry) }}
        </h2>
        <p class="truncate text-[11.5px] text-[var(--muted)]">{{ playbackMetaLine }}</p>
      </div>
      <a
        :href="entryContentUrl(activeFilePlayback.entry, true, activeFilePlayback.source)"
        :download="activeFilePlayback.entry.name"
        class="ctrl-btn no-underline"
        title="下载原文件"
      >
        <AppNavIcon name="download" class="size-3.5" />
        <span class="max-sm:sr-only">下载</span>
      </a>
      <button
        v-if="SHARE_FEATURE_ENABLED"
        type="button"
        class="ctrl-btn"
        title="生成在线播放/文件分享链接"
        @click="openShareForFileEntry(activeFilePlayback.entry, activeFilePlayback.source)"
      >
        <AppNavIcon name="share" class="size-3.5" />
        <span class="max-sm:sr-only">分享</span>
      </button>
      <button type="button" class="ctrl-btn" title="复制流媒体地址给外部播放器使用" @click="copyActiveStreamLink">
        <AppNavIcon name="copy" class="size-3.5" />
        <span class="max-sm:sr-only">复制流地址</span>
      </button>
    </header>

    <div
      class="player-grid"
      :class="{ 'has-playlist': activeFilePlayback.siblings.length > 1 }"
    >
      <div class="min-w-0">
        <ArtPlayerPane
          v-if="activeFilePlayback.info.supportsInAppPlayback || activeFilePlayback.info.supportsTranscodePlayback"
          :gid="activeFilePlayback.info.gid"
          :stream-url="playbackStreamUrl"
          :playback-key="`${activeFilePlayback.info.gid}:${activeFilePlayback.info.deliveryMode}`"
          :expected-duration-seconds="activeFilePlayback.info.durationSeconds"
          :hls="activeFilePlayback.info.deliveryMode === 'hls-remux' || activeFilePlayback.info.deliveryMode === 'hls-transcode'"
          :subtitle-tracks="activeFilePlayback.info.subtitleTracks"
          :subtitle-url-for="playbackSubtitleUrl"
          :history-meta="playbackHistoryMeta"
          @ended="playNextSibling"
        />
        <div v-else class="grid aspect-video place-items-center rounded-lg bg-black px-6 text-center text-xs text-white/75">
          当前文件需安装 FFmpeg 后才能在网页内转码播放，也可点击右上角「复制流地址」在外部播放器打开。
        </div>
      </div>

      <aside v-if="activeFilePlayback.siblings.length > 1" class="playlist-pane">
        <div class="playlist-head">
          <span class="text-xs font-semibold text-[var(--ink)]">同目录可播媒体</span>
          <span class="text-[11px] tabular-nums text-[var(--muted)]">{{ activeFilePlayback.siblings.length }} 个文件</span>
        </div>
        <div class="playlist-list">
          <button
            v-for="sib in activeFilePlayback.siblings"
            :key="sib.id"
            type="button"
            class="playlist-item"
            :class="{ 'is-current': sib.id === activeFilePlayback.entry.id }"
            :title="entryTooltip(sib)"
            @click="sib.id !== activeFilePlayback.entry.id && openFilePlayback(sib, { source: activeFilePlayback.source, entries: activeFilePlayback.siblings, folderLabel: activeFilePlayback.folderLabel })"
          >
            <AppNavIcon :name="iconForKind(sib.kind)" class="size-3.5 shrink-0" />
            <span class="min-w-0 flex-1 truncate text-left">{{ entryLabel(sib) }}</span>
            <span class="shrink-0 text-[10.5px] tabular-nums opacity-70">{{ formatBytes(sib.size) }}</span>
          </button>
        </div>
      </aside>
    </div>
  </div>

  <!-- 文件浏览主界面 -->
  <div v-else class="files-shell">
    <!-- 第一行：本地存储 / Google Drive 云盘切换 + 搜索与操作 -->
    <div class="files-toolbar">
      <div class="source-tabs" role="tablist" aria-label="存储位置">
        <button
          type="button"
          role="tab"
          class="source-tab"
          :class="{ 'is-active': fileSource === 'local' }"
          :aria-selected="fileSource === 'local'"
          @click="switchSource('local')"
        >
          <AppNavIcon name="hard-drive" class="size-3.5" />
          <span>本地存储</span>
          <span v-if="localCapacityBadge" class="source-cap">{{ localCapacityBadge }}</span>
        </button>

        <button
          type="button"
          role="tab"
          class="source-tab"
          :class="{ 'is-active': fileSource === 'cloud' }"
          :aria-selected="fileSource === 'cloud'"
          @click="switchSource('cloud')"
        >
          <span
            class="cloud-dot"
            :class="cloudConnected ? 'is-ok' : 'is-off'"
            aria-hidden="true"
          />
          <span>{{ cloudProviderName.replace(/^Microsoft\s+/iu, "") }}</span>
          <span v-if="cloudCapacityBadge" class="source-cap">{{ cloudCapacityBadge }}</span>
        </button>
      </div>

      <div class="toolbar-right">
        <!-- 云盘已配置专属归档目录时，提供「归档目录 / 全部云盘」快捷切换 -->
        <div
          v-if="fileSource === 'cloud' && cloudConnected && cloudArchiveFolderId !== 'root'"
          class="root-switch"
          role="group"
          aria-label="云盘起始位置"
        >
          <button
            type="button"
            class="root-switch-btn"
            :class="{ 'is-on': !isViewingCloudRoot }"
            @click="switchCloudRootMode('archive')"
          >
            归档目录
          </button>
          <button
            type="button"
            class="root-switch-btn"
            :class="{ 'is-on': isViewingCloudRoot }"
            @click="switchCloudRootMode('root')"
          >
            全部云盘
          </button>
        </div>

        <label class="filter-box">
          <AppNavIcon name="search" class="size-3.5 shrink-0 text-[var(--muted)]" />
          <input
            v-model="filterQuery"
            type="search"
            placeholder="搜索当前目录…"
            aria-label="搜索当前目录文件"
          />
          <button
            v-if="filterQuery"
            type="button"
            class="text-[var(--muted)] hover:text-[var(--ink)]"
            aria-label="清空筛选"
            @click="filterQuery = ''"
          >
            <AppNavIcon name="close" class="size-3" />
          </button>
        </label>

        <button
          v-if="fileSource === 'local' && (localTrashCount > 0 || inLocalTrash)"
          type="button"
          class="ctrl-btn"
          :class="{ 'is-active-pill': inLocalTrash }"
          :title="inLocalTrash ? '返回下载目录' : '查看本地回收站'"
          @click="toggleLocalTrashView"
        >
          <AppNavIcon name="trash" class="size-3.5" />
          <span>回收站</span>
          <span v-if="localTrashCount > 0" class="tabular-nums opacity-80">{{ formatBytes(localTrashBytes) }}</span>
        </button>

        <button
          v-if="inLocalTrash && localTrashCount > 0"
          type="button"
          class="ctrl-btn is-danger-outline"
          :disabled="isBusy('files-empty-trash')"
          @click="confirmingEmptyTrash = true"
        >
          <span>清空回收站</span>
        </button>

        <button
          v-if="SHARE_FEATURE_ENABLED"
          type="button"
          class="ctrl-btn"
          title="查看和管理已分享的链接"
          @click="openShareManager"
        >
          <AppNavIcon name="share" class="size-3.5" />
          <span class="max-sm:sr-only">已分享</span>
          <span v-if="activeSharesCount > 0" class="tabular-nums text-[var(--accent-vivid)]">{{ activeSharesCount }}</span>
        </button>

        <button
          v-if="(fileSource === 'local' && !inLocalTrash) || (fileSource === 'cloud' && cloudConnected)"
          type="button"
          class="ctrl-btn"
          :disabled="currentLoading"
          @click="openCreateFolderBar"
        >
          <AppNavIcon name="folder-plus" class="size-3.5" />
          <span class="max-sm:sr-only">新建目录</span>
        </button>

        <button
          type="button"
          class="ctrl-icon-btn"
          title="刷新当前目录"
          aria-label="刷新当前目录"
          :disabled="currentLoading"
          @click="refreshActiveFiles"
        >
          <AppNavIcon name="refresh" class="size-3.5" :class="{ 'animate-spin': currentLoading }" />
        </button>

        <button
          type="button"
          class="ctrl-btn"
          :title="fileSource === 'local' ? '前往存储与备份设置' : '前往云盘设置'"
          @click="openStorageSettings(fileSource === 'local' ? 'data' : 'download')"
        >
          <AppNavIcon name="settings" class="size-3.5" />
          <span class="max-md:sr-only">{{ fileSource === "local" ? "存储设置" : "云盘设置" }}</span>
        </button>
      </div>
    </div>

    <!-- 第二行：路径面包屑 + 统计与排序 -->
    <div v-if="fileSource === 'local' || cloudConnected" class="crumb-bar">
      <div class="crumb-trail">
        <button
          type="button"
          class="up-btn"
          :disabled="!canGoUp || currentLoading"
          title="返回上级目录"
          @click="goUp"
        >
          <AppNavIcon name="corner-left-up" class="size-3.5" />
          <span>上级</span>
        </button>

        <nav class="crumbs" aria-label="目录路径">
          <template v-if="fileSource === 'local'">
            <template v-for="(crumb, idx) in localCrumbs" :key="crumb.path || '__root'">
              <span v-if="idx > 0" class="crumb-sep">/</span>
              <button
                type="button"
                class="crumb-link"
                :class="{ 'is-current': idx === localCrumbs.length - 1 }"
                :title="idx === 0 && localRootPath ? `本地路径：${localRootPath}` : crumb.label"
                @click="jumpLocalCrumb(crumb.path)"
              >
                {{ crumb.label }}
              </button>
            </template>
            <button
              v-if="localRootPath"
              type="button"
              class="path-copy-btn"
              :title="`复制完整路径：${localRootPath}`"
              @click="copyLocalRootPath"
            >
              <AppNavIcon name="copy" class="size-3" />
            </button>
          </template>

          <template v-else>
            <template v-for="(folder, idx) in cloudTrail" :key="folder.id">
              <span v-if="idx > 0" class="crumb-sep">/</span>
              <button
                type="button"
                class="crumb-link"
                :class="{ 'is-current': idx === cloudTrail.length - 1 }"
                @click="jumpCloudCrumb(folder, idx)"
              >
                {{ folder.name }}
              </button>
            </template>
            <span v-if="cloudQuota?.userEmail" class="cloud-account" :title="cloudQuota.userEmail">
              · {{ cloudQuota.userEmail }}
            </span>
          </template>
        </nav>
      </div>

      <div class="crumb-meta">
        <span class="tabular-nums">{{ summaryStats }}</span>
        <span class="meta-divider" aria-hidden="true">|</span>
        <div class="sort-links" role="group" aria-label="排序方式">
          <button type="button" :class="{ 'is-on': sortField === 'name' }" @click="sortField = 'name'">名称</button>
          <button type="button" :class="{ 'is-on': sortField === 'size' }" @click="sortField = 'size'">大小</button>
          <button type="button" :class="{ 'is-on': sortField === 'time' }" @click="sortField = 'time'">时间</button>
        </div>
      </div>
    </div>

    <!-- 内联新建目录输入条 -->
    <form v-if="creatingFolder" class="mkdir-bar" @submit.prevent="submitNewFolder">
      <AppNavIcon name="folder-plus" class="size-4 shrink-0 text-[var(--accent)]" />
      <input
        ref="newFolderInputRef"
        v-model="newFolderName"
        type="text"
        maxlength="200"
        placeholder="输入新文件夹名称，按回车创建"
        aria-label="新文件夹名称"
        @keydown.esc.stop="creatingFolder = false"
      />
      <button type="submit" class="row-btn is-primary" :disabled="!newFolderName.trim() || isBusy('files-mkdir')">
        创建
      </button>
      <button type="button" class="row-btn" @click="creatingFolder = false">
        取消
      </button>
    </form>

    <!-- 主列表卡片 -->
    <div class="files-table-card">
      <!-- 云盘未连接引导 -->
      <div v-if="fileSource === 'cloud' && cloudConnected === false" class="state-box">
        <p class="text-[13.5px] font-semibold text-[var(--ink)]">未连接 {{ cloudProviderName }}</p>
        <button type="button" class="connect-btn" @click="authorizeCloudFromFiles">
          <AppNavIcon :name="cloudConfigured && settingsForm.cloudProvider !== 'webdav' ? 'external' : 'cloud-upload'" class="size-3.5" />
          <span>{{ cloudConfigured && settingsForm.cloudProvider !== "webdav" ? "浏览器授权" : "前往连接" }}</span>
        </button>
      </div>

      <!-- 错误提示 -->
      <div v-else-if="currentError" class="state-box">
        <p class="text-[13px] font-semibold text-[var(--err)]">读取失败</p>
        <p class="max-w-lg text-xs text-[var(--muted)]">{{ currentError }}</p>
        <button type="button" class="row-btn" @click="refreshActiveFiles">重试</button>
      </div>

      <!-- 首次加载中 -->
      <div v-else-if="currentLoading && !rawEntries.length" class="state-box">
        <p class="text-xs text-[var(--muted)]">读取中…</p>
      </div>

      <!-- 空目录或筛选无结果 -->
      <div v-else-if="!visibleEntries.length" class="state-box">
        <p class="text-[13px] font-semibold text-[var(--muted)]">
          {{ filterQuery ? "无匹配文件" : "空目录" }}
        </p>
      </div>

      <!-- 高密度文件表格 -->
      <div v-else class="files-list">
        <div class="files-row is-header" aria-hidden="true">
          <span class="col-name">名称</span>
          <span class="col-kind">格式</span>
          <span class="col-size">大小</span>
          <span class="col-time">修改时间</span>
          <span class="col-actions">操作</span>
        </div>

        <div
          v-for="entry in visibleEntries"
          :key="entry.id"
          class="files-row is-clickable"
        >
          <button
            type="button"
            class="col-name name-trigger"
            :title="entryTooltip(entry)"
            @click="handleEntryActivate(entry)"
          >
            <AppNavIcon
              :name="iconForKind(entry.kind)"
              class="entry-icon"
              :class="`is-${entry.kind}`"
            />
            <span class="entry-title" :class="{ 'is-folder': entry.isDir }">{{ entryLabel(entry) }}</span>
          </button>

          <span class="col-kind">
            <span class="ext-tag" :class="{ 'is-folder': entry.isDir }">{{ kindBadgeLabel(entry) }}</span>
          </span>

          <span class="col-size tabular-nums">
            {{ entry.isDir && entry.size === 0 ? "—" : formatBytes(entry.size) }}
          </span>

          <span class="col-time tabular-nums">
            {{ formatDateTime(entry.modTime, "—") }}
          </span>

          <div class="col-actions">
            <template v-if="inLocalTrash">
              <button
                v-if="!entry.isDir"
                type="button"
                class="row-btn"
                @click="openPreview(entry)"
              >
                <AppNavIcon name="eye" class="size-3" />
                <span>查看</span>
              </button>
              <a
                v-if="!entry.isDir"
                :href="entryContentUrl(entry, true)"
                :download="entry.name"
                class="act-icon-btn no-underline"
                :title="`下载 ${entryLabel(entry)}`"
                :aria-label="`下载 ${entryLabel(entry)}`"
              >
                <AppNavIcon name="download" class="size-3.5" />
              </a>
              <button
                v-if="localPath === '.trash'"
                type="button"
                class="row-btn"
                :disabled="isBusy(`files-restore-${entry.id}`)"
                @click="restoreLocalTrashEntry(entry)"
              >
                <AppNavIcon name="restore" class="size-3" />
                <span>还原</span>
              </button>
              <button
                type="button"
                class="del-icon-btn"
                title="永久删除"
                :aria-label="`永久删除 ${entry.name}`"
                @click="askDelete(entry)"
              >
                <AppNavIcon name="trash" class="size-3.5" />
              </button>
            </template>

            <template v-else>
              <button
                v-if="entry.isDir"
                type="button"
                class="row-btn"
                @click="handleEntryActivate(entry)"
              >
                打开
              </button>
              <button
                v-else-if="entry.playable"
                type="button"
                class="row-btn is-primary"
                :disabled="isBusy(`files-play-${entry.id}`)"
                @click="openFilePlayback(entry, { entries: visibleEntries })"
              >
                <AppNavIcon name="play" class="size-3" />
                <span>播放</span>
              </button>
              <button
                v-else
                type="button"
                class="row-btn"
                @click="openPreview(entry)"
              >
                <AppNavIcon name="eye" class="size-3" />
                <span>查看</span>
              </button>

              <a
                v-if="!entry.isDir"
                :href="entryContentUrl(entry, true)"
                :download="entry.name"
                class="act-icon-btn no-underline"
                :title="`下载 ${entryLabel(entry)}`"
                :aria-label="`下载 ${entryLabel(entry)}`"
              >
                <AppNavIcon name="download" class="size-3.5" />
              </a>

              <button
                v-if="SHARE_FEATURE_ENABLED"
                type="button"
                class="act-icon-btn"
                :title="`分享 ${entryLabel(entry)}`"
                :aria-label="`分享 ${entryLabel(entry)}`"
                @click="openShareForFileEntry(entry, fileSource)"
              >
                <AppNavIcon name="share" class="size-3.5" />
              </button>
              <button
                type="button"
                class="del-icon-btn"
                :title="fileSource === 'cloud' ? '移入云盘回收站' : '移入回收站 / 删除'"
                :aria-label="`删除 ${entry.name}`"
                @click="askDelete(entry)"
              >
                <AppNavIcon name="trash" class="size-3.5" />
              </button>
            </template>
          </div>
        </div>

        <!-- 云盘分页加载 -->
        <button
          v-if="fileSource === 'cloud' && cloudNextPageToken && !filterQuery"
          type="button"
          class="load-more-btn"
          :disabled="cloudLoadingMore"
          @click="loadMoreCloudFiles"
        >
          {{ cloudLoadingMore ? "正在加载更多云盘条目…" : "加载更多云盘文件 ▾" }}
        </button>
      </div>
    </div>
  </div>

  <!-- 文件预览弹窗（对齐 Diana：图片 / PDF / 文本 / 音视频 / 其他二进制） -->
  <AppDialog
    v-if="preview"
    :title="entryLabel(preview.entry)"
    :description="previewMetaLine"
    size="lg"
    @close="closePreview"
  >
    <div class="file-preview">
      <p v-if="preview.loading" class="text-xs text-[var(--muted)]">读取中…</p>
      <p v-else-if="preview.error" class="text-xs text-[var(--err)]">{{ preview.error }}</p>
      <template v-else-if="preview.kind === 'image'">
        <img :src="preview.url" :alt="preview.entry.name" class="file-preview-img" />
      </template>
      <template v-else-if="preview.kind === 'pdf'">
        <iframe :src="preview.url" :title="preview.entry.name" class="file-preview-frame" />
      </template>
      <template v-else-if="preview.kind === 'video'">
        <video :src="preview.url" controls preload="metadata" class="file-preview-media" />
      </template>
      <template v-else-if="preview.kind === 'audio'">
        <audio :src="preview.url" controls preload="metadata" class="w-full" />
      </template>
      <template v-else-if="preview.kind === 'text'">
        <p v-if="preview.clipped" class="file-preview-warn">
          文件较大，只显示前 256 KB，完整内容请下载。
        </p>
        <pre class="file-preview-text">{{ preview.text || "（空文件）" }}</pre>
      </template>
      <p v-else class="file-preview-other">
        此格式不支持在线预览，可下载后查看。
      </p>
    </div>

    <template #footer>
      <div class="mr-auto flex items-center gap-2">
        <button
          type="button"
          class="row-btn is-danger-ghost !h-8 !px-3 !text-xs"
          @click="askDelete(preview.entry)"
        >
          <AppNavIcon name="trash" class="size-3.5" />
          <span>{{ (preview.source === "local" && !inLocalTrash) || (preview.source === "cloud" && settingsForm.cloudProvider !== "webdav") ? "移到回收站" : "删除" }}</span>
        </button>
        <button
          v-if="SHARE_FEATURE_ENABLED && !inLocalTrash"
          type="button"
          class="row-btn !h-8 !px-3 !text-xs"
          @click="openShareForFileEntry(preview.entry, preview.source)"
        >
          <AppNavIcon name="share" class="size-3.5" />
          <span>分享</span>
        </button>
      </div>

      <button
        v-if="preview.kind === 'text' && preview.text && !preview.loading"
        type="button"
        class="row-btn !h-8 !px-3 !text-xs"
        @click="copyPreviewText"
      >
        <AppNavIcon name="copy" class="size-3.5" />
        <span>复制内容</span>
      </button>
      <a
        :href="preview.downloadUrl"
        :download="preview.entry.name"
        class="btn-primary inline-flex h-8 items-center gap-1.5 px-3.5 text-xs font-semibold no-underline"
      >
        <AppNavIcon name="download" class="size-3.5" />
        <span>下载</span>
      </a>
    </template>
  </AppDialog>

  <!-- 删除 / 移入回收站确认弹窗 -->
  <AppDialog
    v-if="pendingDelete"
    :title="deleteWillTrash ? `删除${pendingDelete.isDir ? '目录' : '文件'}` : `永久删除${pendingDelete.isDir ? '目录' : '文件'}`"
    size="sm"
    :dismissible="!isBusy(`files-del-${pendingDelete.id}`)"
    @close="pendingDelete = null"
  >
    <p class="text-sm leading-6 text-[var(--muted)]">
      <strong class="text-[var(--ink)]">「{{ entryLabel(pendingDelete) }}」</strong>
      <template v-if="fileSource === 'local' && !inLocalTrash">
        将移入本地回收站，在清空回收站前可随时还原。
      </template>
      <template v-else-if="fileSource === 'cloud' && settingsForm.cloudProvider !== 'webdav'">
        将移入 {{ cloudProviderName }} 回收站。
      </template>
      <template v-else>
        将被永久删除，无法恢复。
      </template>
    </p>
    <template #footer>
      <button
        v-if="fileSource === 'local' && !inLocalTrash"
        type="button"
        class="mr-auto text-xs font-medium text-[var(--err)] opacity-85 hover:opacity-100"
        :disabled="isBusy(`files-del-${pendingDelete.id}`)"
        @click="runConfirmedDelete(true)"
      >
        直接永久删除
      </button>
      <button
        type="button"
        class="row-btn !h-8 !px-3.5 !text-xs"
        :disabled="isBusy(`files-del-${pendingDelete.id}`)"
        @click="pendingDelete = null"
      >
        取消
      </button>
      <button
        type="button"
        class="row-btn is-danger !h-8 !px-3.5 !text-xs"
        :disabled="isBusy(`files-del-${pendingDelete.id}`)"
        @click="runConfirmedDelete(false)"
      >
        {{ deleteWillTrash ? "移到回收站" : "永久删除" }}
      </button>
    </template>
  </AppDialog>

  <!-- 清空本地回收站确认弹窗 -->
  <AppDialog
    v-if="confirmingEmptyTrash"
    title="清空回收站"
    size="sm"
    :dismissible="!isBusy('files-empty-trash')"
    @close="confirmingEmptyTrash = false"
  >
    <p class="text-sm leading-6 text-[var(--muted)]">
      回收站内的 <strong class="text-[var(--ink)]">{{ localTrashCount }} 个项目（{{ formatBytes(localTrashBytes) }}）</strong> 将被永久删除，无法恢复。
    </p>
    <template #footer>
      <button
        type="button"
        class="row-btn !h-8 !px-3.5 !text-xs"
        :disabled="isBusy('files-empty-trash')"
        @click="confirmingEmptyTrash = false"
      >
        取消
      </button>
      <button
        type="button"
        class="row-btn is-danger !h-8 !px-3.5 !text-xs"
        :disabled="isBusy('files-empty-trash')"
        @click="runEmptyTrash"
      >
        永久删除
      </button>
    </template>
  </AppDialog>
</template>

<style scoped>
.files-shell {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

/* 顶栏：左侧下划线 Tab + 右侧紧凑工具条 */
.files-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 12px;
  min-height: 36px;
  border-bottom: 1px solid var(--line);
}

.source-tabs {
  display: flex;
  align-items: stretch;
  gap: 16px;
}

.source-tab {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 36px;
  padding: 0 2px;
  color: var(--ink-secondary);
  font-size: 13.5px;
  font-weight: 550;
  white-space: nowrap;
  transition: color 0.15s;
}
.source-tab:hover {
  color: var(--ink);
}
.source-tab.is-active {
  color: var(--ink);
  font-weight: 650;
}
.source-tab.is-active::after {
  content: "";
  position: absolute;
  left: 0;
  right: 0;
  bottom: -1px;
  height: 2px;
  border-radius: 1px;
  background: var(--accent-vivid);
}

.source-cap {
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--panel);
  color: var(--muted);
  font-size: 11px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.source-tab.is-active .source-cap {
  background: var(--accent-soft);
  color: var(--accent);
}

.cloud-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.cloud-dot.is-ok {
  background: var(--ok);
}
.cloud-dot.is-off {
  background: var(--muted);
  opacity: 0.6;
}

.toolbar-right {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding-bottom: 4px;
}

.root-switch {
  display: inline-flex;
  align-items: center;
  padding: 2px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--panel);
}
.root-switch-btn {
  height: 30px;
  padding: 0 11px;
  border-radius: 5px;
  font-size: 11.5px;
  font-weight: 550;
  color: var(--ink-secondary);
  transition: background 0.15s, color 0.15s;
}
.root-switch-btn.is-on {
  background: var(--card);
  color: var(--ink);
  font-weight: 600;
  box-shadow: var(--soft-shadow);
}

.filter-box {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  width: 11.5rem;
  height: 34px;
  padding: 0 10px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--card);
  transition: border-color 0.15s;
}
.filter-box:focus-within {
  border-color: var(--accent-line);
}
.filter-box input {
  width: 100%;
  min-width: 0;
  background: transparent;
  font-size: 12px;
  color: var(--ink);
  outline: none;
}

.ctrl-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 11px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink-secondary);
  font-size: 12px;
  font-weight: 550;
  white-space: nowrap;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
.ctrl-btn:hover:not(:disabled) {
  border-color: var(--accent-line);
  color: var(--ink);
}
.ctrl-btn.is-active-pill {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent-vivid);
  font-weight: 600;
}
.ctrl-btn.is-danger-outline {
  border-color: color-mix(in srgb, var(--err) 35%, var(--line));
  color: var(--err);
}
.ctrl-btn.is-danger-outline:hover:not(:disabled) {
  background: var(--err-soft);
  border-color: var(--err);
  color: var(--err);
}

.ctrl-icon-btn {
  display: inline-grid;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink-secondary);
  transition: border-color 0.15s, color 0.15s;
}
.ctrl-icon-btn:hover:not(:disabled) {
  border-color: var(--accent-line);
  color: var(--ink);
}

/* 第二行：面包屑与计数排序 */
.crumb-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px 12px;
  min-height: 26px;
  font-size: 12px;
}

.crumb-trail {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: center;
  gap: 8px;
}

.up-btn {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
  height: 32px;
  padding: 0 10px;
  border-radius: 6px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 11.5px;
  font-weight: 550;
  transition: color 0.15s, background 0.15s;
}
.up-btn:hover:not(:disabled) {
  color: var(--ink);
  background: var(--tint-2);
}
.up-btn:disabled {
  opacity: 0.4;
}

.crumbs {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
}
.crumb-sep {
  color: var(--muted);
  opacity: 0.6;
}
.crumb-link {
  max-width: 14rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  border-radius: 4px;
  padding: 1px 4px;
  color: var(--ink-secondary);
  font-weight: 500;
  transition: color 0.15s, background 0.15s;
}
.crumb-link:hover {
  background: var(--panel);
  color: var(--ink);
}
.crumb-link.is-current {
  color: var(--ink);
  font-weight: 600;
}

.path-copy-btn {
  display: inline-grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: 4px;
  color: var(--muted);
}
.path-copy-btn:hover {
  background: var(--panel);
  color: var(--ink);
}

.cloud-account {
  max-width: 12rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
  font-size: 11.5px;
}

.crumb-meta {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 11.5px;
}
.meta-divider {
  opacity: 0.4;
}
.sort-links {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.sort-links button {
  color: var(--muted);
  font-weight: 500;
  transition: color 0.15s;
}
.sort-links button:hover {
  color: var(--ink);
}
.sort-links button.is-on {
  color: var(--accent-vivid);
  font-weight: 600;
}

/* 内联新建文件夹栏 */
.mkdir-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 8px;
  border: 1px solid var(--accent-line);
  background: var(--card);
}
.mkdir-bar input {
  flex: 1;
  min-width: 0;
  background: transparent;
  font-size: 12.5px;
  color: var(--ink);
  outline: none;
}

/* 主文件表格：行数少时紧凑包裹不撑出大块空白，行数多时在视口内纵向滚动 */
.files-table-card {
  flex: 0 1 auto;
  min-height: 0;
  overflow-y: auto;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--card);
}

.state-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 176px;
  padding: 24px 16px;
  text-align: center;
}

.connect-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  margin-top: 4px;
  padding: 0 14px;
  border-radius: 7px;
  background: var(--accent);
  color: var(--accent-contrast);
  font-size: 12.5px;
  font-weight: 600;
  transition: background 0.15s;
}
.connect-btn:hover {
  background: var(--accent-strong);
}

.files-list {
  display: flex;
  flex-direction: column;
}

.files-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 4.25rem 5.5rem 7.25rem 11.25rem;
  align-items: center;
  gap: 10px;
  min-height: 38px;
  padding: 5px 12px;
  border-bottom: 1px solid var(--line);
  font-size: 12.5px;
  transition: background 0.12s;
}
.files-row:last-child {
  border-bottom: none;
}
.files-row:not(.is-header):hover {
  background: color-mix(in srgb, var(--panel) 65%, transparent);
}

.files-row.is-header {
  position: sticky;
  top: 0;
  z-index: 2;
  min-height: 30px;
  padding-block: 4px;
  background: var(--card);
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}

.col-name {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  text-align: left;
}
.name-trigger:hover .entry-title {
  color: var(--accent-vivid);
}

.entry-icon {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  color: var(--muted);
}
.entry-icon.is-folder {
  color: var(--accent);
}
.entry-icon.is-video {
  color: var(--accent-vivid);
}
.entry-icon.is-audio {
  color: var(--ok);
}
.entry-icon.is-image {
  color: #38bdf8;
}
.entry-icon.is-pdf,
.entry-icon.is-text,
.entry-icon.is-subtitle {
  color: var(--ink-secondary);
}

.entry-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ink);
  font-weight: 500;
  transition: color 0.12s;
}
.entry-title.is-folder {
  font-weight: 600;
}

.col-kind {
  display: flex;
  align-items: center;
}
.ext-tag {
  display: inline-flex;
  align-items: center;
  height: 19px;
  padding: 0 5px;
  border-radius: 4px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10.5px;
  font-weight: 600;
}
.ext-tag.is-folder {
  font-family: inherit;
  background: var(--accent-soft);
  color: var(--accent);
}

.col-size {
  text-align: right;
  color: var(--ink-secondary);
  font-size: 12px;
}

.col-time {
  text-align: right;
  color: var(--muted);
  font-size: 11.5px;
}

.col-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 5px;
}

.row-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 32px;
  padding: 0 10px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink-secondary);
  font-size: 11.5px;
  font-weight: 600;
  white-space: nowrap;
  transition: background 0.12s, border-color 0.12s, color 0.12s;
}
.row-btn:hover:not(:disabled) {
  border-color: var(--accent-line);
  color: var(--ink);
}
.row-btn.is-primary {
  border-color: transparent;
  background: var(--accent);
  color: var(--accent-contrast);
}
.row-btn.is-primary:hover:not(:disabled) {
  background: var(--accent-strong);
  color: var(--accent-contrast);
}
.row-btn.is-danger {
  border-color: transparent;
  background: var(--err);
  color: #fff;
}
.row-btn.is-danger-ghost {
  border-color: color-mix(in srgb, var(--err) 35%, var(--line));
  background: transparent;
  color: var(--err);
}
.row-btn.is-danger-ghost:hover:not(:disabled) {
  border-color: var(--err);
  background: var(--err-soft);
  color: var(--err);
}

/* 文件预览弹窗（对齐 Diana） */
.file-preview {
  display: grid;
  gap: 10px;
  min-width: 0;
}

.file-preview-img {
  display: block;
  max-width: 100%;
  max-height: 68vh;
  margin: 0 auto;
  border-radius: 8px;
  background: var(--panel);
}

.file-preview-frame {
  display: block;
  width: 100%;
  height: 68vh;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
}

.file-preview-media {
  display: block;
  width: 100%;
  max-height: 68vh;
  border-radius: 8px;
  background: #000;
}

.file-preview-warn {
  margin: 0;
  padding: 7px 11px;
  border-radius: 7px;
  background: var(--warn-soft);
  color: var(--warn);
  font-size: 12px;
}

.file-preview-text {
  margin: 0;
  max-height: 62vh;
  overflow: auto;
  padding: 12px 14px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--panel);
  color: var(--ink);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.file-preview-other {
  margin: 0;
  padding: 20px 14px;
  border-radius: 8px;
  background: var(--panel);
  color: var(--muted);
  font-size: 13px;
  text-align: center;
}

.act-icon-btn,
.del-icon-btn {
  display: inline-grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  color: var(--muted);
  transition: background 0.12s, color 0.12s;
}
.act-icon-btn:hover {
  background: var(--accent-soft);
  color: var(--accent-vivid);
}
.del-icon-btn:hover {
  background: var(--err-soft);
  color: var(--err);
}

.confirm-hint {
  color: var(--err);
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}

.load-more-btn {
  width: 100%;
  height: 36px;
  color: var(--accent-vivid);
  font-size: 12px;
  font-weight: 600;
  text-align: center;
  transition: background 0.15s;
}
.load-more-btn:hover:not(:disabled) {
  background: var(--panel);
}

/* 在线播放器容器 */
.files-player {
  display: flex;
  flex-direction: column;
  gap: 10px;
  height: 100%;
  min-height: 0;
  overflow-y: auto;
}

.player-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.player-grid {
  display: grid;
  gap: 12px;
  align-items: start;
}
@media (min-width: 960px) {
  .player-grid.has-playlist {
    grid-template-columns: minmax(0, 1fr) 17.5rem;
  }
}

.playlist-pane {
  display: flex;
  flex-direction: column;
  max-height: 28rem;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--card);
  overflow: hidden;
}
.playlist-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--line);
}
.playlist-list {
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  padding: 4px;
}
.playlist-item {
  display: flex;
  align-items: center;
  gap: 7px;
  min-height: 32px;
  padding: 4px 8px;
  border-radius: 6px;
  color: var(--ink-secondary);
  font-size: 12px;
  transition: background 0.12s, color 0.12s;
}
.playlist-item:hover {
  background: var(--panel);
  color: var(--ink);
}
.playlist-item.is-current {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

@media (max-width: 760px) {
  .files-row {
    grid-template-columns: minmax(0, 1fr) 4.8rem 8rem;
  }
  .col-kind,
  .col-time {
    display: none;
  }
  .filter-box {
    width: 9.5rem;
  }
}
</style>
