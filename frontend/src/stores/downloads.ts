import { computed, reactive, ref } from "vue";

import type {
  DownloadConflictStrategy,
  DownloadJob,
  DownloadPlaybackInfo,
  ExternalPlayerId,
  SourceType
} from "@shared/index";

import {
  archiveDownload,
  cancelDownloadArchive,
  clearFinishedDownloads,
  createDownload,
  createDownloadsBatch,
  getDownloadEventsUrl,
  getDownloadPlaybackInfo,
  listDownloads,
  openDownloadPlayer,
  pauseDownload,
  removeDownload,
  resolveDownloadConflict,
  resumeDownload
} from "../api";
import { t } from "../i18n";
import { isCloudStored, isCloudTransferring } from "../lib/cloudArchive";
import { downloadDetailSearchKeyword, downloadDisplayTitle } from "../lib/downloadTitle";
import { isBusyMatching, withBusy } from "./busy";
import { openDownloadAnimeDetail } from "./discovery";
import { playbackRouteLabel, readPlaybackGidFromLocation, syncLocationWithTab } from "./router";
import { feedItemById, refreshFeedItems } from "./subscriptions";
import { toastError, updateFeedback } from "./toasts";

export const downloads = ref<DownloadJob[]>([]);
export const selectedDownloadDetailGid = ref<string | null>(null);
export const activePlayback = ref<DownloadPlaybackInfo | null>(null);
export const sseConnected = ref(false);

export const manualDownloadForm = reactive<{
  url: string;
  sourceTypeOverride: SourceType | null;
  saveTo: "local" | "cloud";
  /** 空字符串表示用设置里的下载目录 */
  directory: string;
}>({
  url: "",
  sourceTypeOverride: null,
  saveTo: "local",
  directory: ""
});

function inferSourceTypeFromUrl(rawUrl: string): SourceType {
  const value = rawUrl.trim().toLowerCase();
  if (value.startsWith("magnet:")) {
    return "magnet";
  }
  if (value.endsWith(".torrent") || /\.torrent(?:\?|#|$)/i.test(value)) {
    return "torrent-url";
  }
  return "direct";
}

export function extractManualUrls(raw: string): string[] {
  const lines = raw
    .split(/\r?\n/u)
    .map((line) => line.trim())
    .filter(Boolean);
  if (!lines.length) {
    return [];
  }
  if (lines.length === 1) {
    const embedded = lines[0]!.match(/(?:magnet:\?[^\s"'<>]+|https?:\/\/[^\s"'<>]+)/giu);
    if (embedded && embedded.length > 1) {
      return Array.from(new Set(embedded.map((u) => u.trim())));
    }
    return [lines[0]!];
  }
  const seen = new Set<string>();
  const result: string[] = [];
  for (const line of lines) {
    const matches = line.match(/(?:magnet:\?[^\s"'<>]+|https?:\/\/[^\s"'<>]+)/giu);
    if (matches?.length) {
      for (const m of matches) {
        const clean = m.trim();
        if (clean && !seen.has(clean)) {
          seen.add(clean);
          result.push(clean);
        }
      }
    } else if (!seen.has(line)) {
      seen.add(line);
      result.push(line);
    }
  }
  return result;
}

export const parsedManualUrls = computed<string[]>(() => extractManualUrls(manualDownloadForm.url));

export const inferredManualSourceType = computed<SourceType>(() =>
  inferSourceTypeFromUrl(parsedManualUrls.value[0] ?? manualDownloadForm.url)
);

export const onlineDownloadCount = computed(
  () =>
    downloads.value.filter(
      (item) => item.status === "active" || item.status === "waiting" || isCloudTransferring(item)
    ).length
);
export const finishedDownloadCount = computed(
  () => downloads.value.filter((item) => item.status === "complete").length
);
export const totalDownloadSpeed = computed(() =>
  downloads.value.reduce((sum, item) => sum + (item.status === "active" ? item.downloadSpeed : 0), 0)
);
export const totalUploadSpeed = computed(() =>
  downloads.value.reduce((sum, item) => sum + (item.status === "active" ? item.uploadSpeed : 0), 0)
);
export const totalCloudUploadSpeed = computed(() =>
  downloads.value.reduce(
    (sum, item) => sum + (item.cloudArchive?.status === "uploading" ? (item.cloudArchive.uploadSpeed ?? 0) : 0),
    0
  )
);

export function displayTitle(download: DownloadJob): string {
  return downloadDisplayTitle(download, feedItemById.value);
}

export function detailSearchKeyword(download: DownloadJob): string {
  return downloadDetailSearchKeyword(download, feedItemById.value);
}

/** 从发布名里拆出的番名：「[FX战士久留美 / FX Senshi Kurumi-chan」→「FX战士久留美」，拆不出来才用整条发布名 */
export function shortAnimeTitle(download: DownloadJob): string {
  const name = detailSearchKeyword(download).replace(/[[\]【】]/gu, " ").split(/\s+\/\s+/u)[0]?.trim();
  return name || displayTitle(download);
}

export function downloadRawError(download: DownloadJob): string | null {
  if (!download.errorMessage || download.issueKind === "connected-idle") {
    return null;
  }
  return download.errorMessage;
}

export function isDownloadRowBusy(gid: string): boolean {
  return isBusyMatching(
    (key) =>
      key === `download:pause:${gid}` ||
      key === `download:resume:${gid}` ||
      key === `download:remove:${gid}` ||
      key === `download:archive:${gid}` ||
      key === `download:cancel-archive:${gid}` ||
      key === `download:playback:${gid}` ||
      key.startsWith(`download:player:${gid}:`) ||
      (key.startsWith(`download:conflict:`) && key.endsWith(`:${gid}`))
  );
}

/**
 * 已完成但用户还没去看过的下载数量，用于侧栏「下载管理」未读角标。
 * 完成事件由 SSE 实时推送与 refreshDownloads 快照对比共同驱动。
 */
export const unreadCompletedDownloadCount = ref(0);

export function clearUnreadCompletedDownloads(): void {
  unreadCompletedDownloadCount.value = 0;
}

interface TransferSmoothState {
  status: DownloadJob["status"];
  totalLength: number;
  ewmaDown: number;
  ewmaUp: number;
  ewmaCloudUp: number;
  committedDown: number;
  committedUp: number;
  committedCloudUp: number;
  lastSpeedCommitAt: number;
}

const transferSmoothByGid = new Map<string, TransferSmoothState>();
const SPEED_COMMIT_INTERVAL_MS = 1000;

function stabilizeDownloadJob(item: DownloadJob, prev: DownloadJob | undefined, now: number): DownloadJob {
  if (item.status !== "active" && !isCloudTransferring(item)) {
    transferSmoothByGid.delete(item.gid);
    return item;
  }

  let completedLength = item.completedLength;
  if (
    prev &&
    prev.status === "active" &&
    item.status === "active" &&
    item.totalLength > 0 &&
    prev.totalLength === item.totalLength
  ) {
    completedLength = Math.min(item.totalLength, Math.max(prev.completedLength, item.completedLength));
  }

  let files = item.files;
  if (
    prev?.cloudArchive?.status === "uploading" &&
    item.cloudArchive?.status === "uploading" &&
    item.files?.length &&
    prev.files?.length === item.files.length
  ) {
    files = item.files.map((file, idx) => {
      const prevFile = prev.files?.[idx];
      if (!prevFile || prevFile.path !== file.path) {
        return file;
      }
      const prevUploaded = prevFile.uploadedBytes ?? 0;
      const curUploaded = file.uploadedBytes ?? 0;
      if (curUploaded >= prevUploaded) {
        return file;
      }
      const cap = file.size && file.size > 0 ? file.size : Number.MAX_SAFE_INTEGER;
      return {
        ...file,
        uploadedBytes: Math.min(cap, prevUploaded)
      };
    });
  }

  const rawDown = Math.max(0, item.downloadSpeed);
  const rawUp = Math.max(0, item.uploadSpeed);
  const rawCloudUp = item.cloudArchive?.status === "uploading" ? Math.max(0, item.cloudArchive.uploadSpeed ?? 0) : 0;

  let state = transferSmoothByGid.get(item.gid);
  if (
    !state ||
    state.status !== item.status ||
    (item.totalLength > 0 && state.totalLength > 0 && state.totalLength !== item.totalLength)
  ) {
    state = {
      status: item.status,
      totalLength: item.totalLength,
      ewmaDown: rawDown,
      ewmaUp: rawUp,
      ewmaCloudUp: rawCloudUp,
      committedDown: rawDown,
      committedUp: rawUp,
      committedCloudUp: rawCloudUp,
      lastSpeedCommitAt: now
    };
    transferSmoothByGid.set(item.gid, state);
  } else {
    state.totalLength = item.totalLength || state.totalLength;
    state.ewmaDown = rawDown > 0 ? (state.ewmaDown > 0 ? state.ewmaDown * 0.7 + rawDown * 0.3 : rawDown) : state.ewmaDown >= 4096 ? state.ewmaDown * 0.65 : 0;
    state.ewmaUp = rawUp > 0 ? (state.ewmaUp > 0 ? state.ewmaUp * 0.7 + rawUp * 0.3 : rawUp) : state.ewmaUp >= 2048 ? state.ewmaUp * 0.65 : 0;
    state.ewmaCloudUp =
      rawCloudUp > 0
        ? state.ewmaCloudUp > 0
          ? state.ewmaCloudUp * 0.7 + rawCloudUp * 0.3
          : rawCloudUp
        : state.ewmaCloudUp >= 4096
          ? state.ewmaCloudUp * 0.65
          : 0;

    const shouldCommitImmediately =
      (state.committedDown === 0 && state.ewmaDown > 0) ||
      (state.committedDown > 0 && state.ewmaDown === 0) ||
      (state.committedUp === 0 && state.ewmaUp > 0) ||
      (state.committedUp > 0 && state.ewmaUp === 0) ||
      (state.committedCloudUp === 0 && state.ewmaCloudUp > 0) ||
      (state.committedCloudUp > 0 && state.ewmaCloudUp === 0);

    if (shouldCommitImmediately || now - state.lastSpeedCommitAt >= SPEED_COMMIT_INTERVAL_MS) {
      state.committedDown = Math.round(state.ewmaDown);
      state.committedUp = Math.round(state.ewmaUp);
      state.committedCloudUp = Math.round(state.ewmaCloudUp);
      state.lastSpeedCommitAt = now;
    }
  }

  const cloudArchive =
    item.cloudArchive?.status === "uploading"
      ? {
          ...item.cloudArchive,
          uploadSpeed: state.committedCloudUp
        }
      : item.cloudArchive;

  return {
    ...item,
    completedLength,
    downloadSpeed: item.status === "active" ? state.committedDown : item.downloadSpeed,
    uploadSpeed: item.status === "active" ? state.committedUp : item.uploadSpeed,
    files,
    cloudArchive
  };
}

export function hydrateDownloads(next: DownloadJob[]): void {
  applyDownloadsSnapshot(next);
}

function applyDownloadsSnapshot(next: DownloadJob[]): void {
  const previousByGid = new Map(downloads.value.map((item) => [item.gid, item]));
  const now = Date.now();
  const nextGids = new Set(next.map((item) => item.gid));
  for (const gid of transferSmoothByGid.keys()) {
    if (!nextGids.has(gid)) {
      transferSmoothByGid.delete(gid);
    }
  }
  const stabilized = next.map((item) => stabilizeDownloadJob(item, previousByGid.get(item.gid), now));
  downloads.value = stabilized;

  // 首次加载（previous 为空）不算完成事件，否则每次刷新页面都会炸一堆 toast。
  if (!previousByGid.size) {
    return;
  }

  const justCompleted = next.filter((item) => {
    const prev = previousByGid.get(item.gid);
    return item.status === "complete" && prev !== undefined && prev.status !== "complete";
  });
  if (justCompleted.length) {
    unreadCompletedDownloadCount.value += justCompleted.length;
    if (justCompleted.length === 1) {
      updateFeedback(`下载完成：${displayTitle(justCompleted[0]!)}`, "success");
    } else {
      updateFeedback(`${justCompleted.length} 个下载已完成。`, "success");
    }
  }

  const justArchived = next.filter((item) => {
    const prev = previousByGid.get(item.gid);
    return isCloudStored(item) && prev !== undefined && !isCloudStored(prev) && prev.cloudArchive?.status === "uploading";
  });
  if (justArchived.length) {
    if (justArchived.length === 1) {
      updateFeedback(`已转存至云盘：${displayTitle(justArchived[0]!)}`, "success");
    } else {
      updateFeedback(`${justArchived.length} 个任务已转存至云盘。`, "success");
    }
  }
}

export async function refreshDownloads(): Promise<void> {
  const next = await listDownloads();
  applyDownloadsSnapshot(next);
}

/* ---------- SSE 实时流（亚秒级下载与云盘上传速度推送，断线自动重连） ---------- */

let downloadEventSource: EventSource | null = null;
let sseReconnectTimer: number | null = null;
let sseShouldRun = false;
let sseRetryAttempt = 0;

function clearSseReconnectTimer(): void {
  if (sseReconnectTimer !== null) {
    window.clearTimeout(sseReconnectTimer);
    sseReconnectTimer = null;
  }
}

function scheduleSseReconnect(): void {
  clearSseReconnectTimer();
  if (!sseShouldRun) {
    return;
  }
  const delayMs = Math.min(12000, 1500 * Math.pow(2, sseRetryAttempt));
  sseRetryAttempt = Math.min(sseRetryAttempt + 1, 4);
  sseReconnectTimer = window.setTimeout(() => {
    sseReconnectTimer = null;
    if (sseShouldRun) {
      openDownloadEventSource();
    }
  }, delayMs);
}

function openDownloadEventSource(): void {
  if (import.meta.env.VITE_DEMO_MODE === "true" || typeof EventSource === "undefined" || downloadEventSource) {
    return;
  }
  try {
    const source = new EventSource(getDownloadEventsUrl());
    downloadEventSource = source;
    source.onopen = () => {
      sseConnected.value = true;
      sseRetryAttempt = 0;
    };
    source.addEventListener("downloads", (event) => {
      try {
        const parsed = JSON.parse((event as MessageEvent).data);
        if (Array.isArray(parsed)) {
          applyDownloadsSnapshot(parsed as DownloadJob[]);
        }
      } catch {
        // 忽略偶发异常帧，等待下一帧或轮询兜底
      }
    });
    source.onerror = () => {
      sseConnected.value = false;
      source.close();
      if (downloadEventSource === source) {
        downloadEventSource = null;
      }
      scheduleSseReconnect();
    };
  } catch {
    sseConnected.value = false;
    scheduleSseReconnect();
  }
}

export function connectDownloadEvents(): void {
  sseShouldRun = true;
  clearSseReconnectTimer();
  openDownloadEventSource();
}

export function disconnectDownloadEvents(): void {
  sseShouldRun = false;
  sseConnected.value = false;
  clearSseReconnectTimer();
  if (downloadEventSource) {
    downloadEventSource.close();
    downloadEventSource = null;
  }
}

function upsertDownloadRow(updated: DownloadJob | null | undefined): void {
  if (!updated?.gid) {
    return;
  }
  const idx = downloads.value.findIndex((item) => item.gid === updated.gid);
  if (idx >= 0) {
    const copy = [...downloads.value];
    copy[idx] = updated;
    downloads.value = copy;
  } else {
    downloads.value = [updated, ...downloads.value];
  }
}

export async function submitManualDownload(): Promise<void> {
  const urls = parsedManualUrls.value;
  if (!urls.length) {
    updateFeedback("请先填写下载链接。", "error");
    return;
  }
  const directory =
    manualDownloadForm.saveTo === "local" && manualDownloadForm.directory.trim()
      ? manualDownloadForm.directory.trim()
      : undefined;

  await withBusy("download:manual", async () => {
    try {
      if (urls.length === 1) {
        const created = await createDownload({
          url: urls[0]!,
          sourceType: manualDownloadForm.sourceTypeOverride ?? inferredManualSourceType.value,
          saveTo: manualDownloadForm.saveTo,
          ...(directory ? { directory } : {})
        });
        upsertDownloadRow(created);
        manualDownloadForm.url = "";
        manualDownloadForm.sourceTypeOverride = null;
        updateFeedback(t("downloads.manualQueued"), "success");
      } else {
        const result = await createDownloadsBatch({
          saveTo: manualDownloadForm.saveTo,
          ...(directory ? { directory } : {}),
          items: urls.map((url) => ({
            url,
            sourceType: manualDownloadForm.sourceTypeOverride ?? inferSourceTypeFromUrl(url)
          }))
        });
        for (const item of result.items ?? []) {
          upsertDownloadRow(item);
        }
        manualDownloadForm.url = "";
        manualDownloadForm.sourceTypeOverride = null;
        if (result.failedCount > 0) {
          updateFeedback(
            `已添加 ${result.queuedCount} 条，跳过 ${result.skippedCount} 条，失败 ${result.failedCount} 条。`,
            result.queuedCount > 0 ? "info" : "error"
          );
        } else {
          updateFeedback(
            result.skippedCount > 0
              ? `已批量添加 ${result.queuedCount} 条链接（跳过 ${result.skippedCount} 条重复项）。`
              : `已批量添加 ${result.queuedCount} 条下载任务。`,
            "success"
          );
        }
      }
      await Promise.allSettled([refreshDownloads(), refreshFeedItems()]);
    } catch (error) {
      toastError(error, t("downloads.manualFailed"));
    }
  });
}

export async function updateDownloadState(
  action: "pause" | "resume" | "remove",
  gid: string,
  options: { deleteLocalFiles?: boolean; silent?: boolean; skipRefresh?: boolean } = {}
): Promise<boolean> {
  return withBusy(`download:${action}:${gid}`, async () => {
    const snapshot = downloads.value;
    const targetIndex = snapshot.findIndex((item) => item.gid === gid);
    const previousItem = targetIndex >= 0 ? snapshot[targetIndex]! : null;

    // 乐观更新：按钮在 1 帧内响应，无需等待后端 RPC 来回
    if (previousItem) {
      if (action === "pause") {
        upsertDownloadRow({ ...previousItem, status: "paused", downloadSpeed: 0, uploadSpeed: 0 });
      } else if (action === "resume") {
        upsertDownloadRow({ ...previousItem, status: "waiting", errorMessage: null });
      } else if (action === "remove") {
        downloads.value = snapshot.filter((item) => item.gid !== gid);
      }
    }

    try {
      if (action === "pause") {
        const updated = await pauseDownload(gid);
        if (updated) upsertDownloadRow(updated);
        if (!options.silent) {
          updateFeedback(t("downloads.pauseCompleted"), "success");
        }
      } else if (action === "resume") {
        const updated = await resumeDownload(gid);
        if (updated) upsertDownloadRow(updated);
        if (!options.silent) {
          updateFeedback(t("downloads.resumeCompleted"), "success");
        }
      } else {
        const deleteLocalFiles = options.deleteLocalFiles ?? false;
        const result = await removeDownload(gid, { deleteLocalFiles });
        if (!options.silent) {
          updateFeedback(
            deleteLocalFiles
              ? t("downloads.removeWithFiles", { count: result.deletedLocalFileCount })
              : t("downloads.removeKeepFiles"),
            "success"
          );
        }
        if (selectedDownloadDetailGid.value === gid) {
          selectedDownloadDetailGid.value = null;
        }
        if (activePlayback.value?.gid === gid) {
          closePlayback();
        }
      }
      if (!options.skipRefresh && !sseConnected.value) {
        await refreshDownloads();
      }
      return true;
    } catch (error) {
      downloads.value = snapshot;
      if (!options.silent) {
        toastError(error, t("downloads.updateFailed"));
      }
      return false;
    }
  });
}

export async function triggerCloudArchive(
  gid: string,
  options: { silent?: boolean; skipRefresh?: boolean } = {}
): Promise<boolean> {
  return withBusy(`download:archive:${gid}`, async () => {
    const snapshot = downloads.value;
    const prev = snapshot.find((item) => item.gid === gid);
    const wasRetry = prev?.cloudArchive?.status === "error";
    if (prev) {
      upsertDownloadRow({
        ...prev,
        saveTo: "cloud",
        cloudArchive:
          prev.status === "complete"
            ? { status: "uploading", error: null, retryAt: 0, uploadSpeed: 0 }
            : prev.cloudArchive ?? null
      });
    }
    try {
      const updated = await archiveDownload(gid);
      upsertDownloadRow(updated);
      if (!options.silent) {
        updateFeedback(
          updated.status === "complete"
            ? wasRetry
              ? "已立即重试云盘转存。"
              : "已开始转存至 Google Drive。"
            : "已标记完成后自动转存至 Google Drive。",
          "success"
        );
      }
      if (!options.skipRefresh && !sseConnected.value) {
        await refreshDownloads();
      }
      return true;
    } catch (error) {
      downloads.value = snapshot;
      if (!options.silent) {
        toastError(error, "触发云盘转存失败，请检查「设置 → 云盘归档」连接状态。");
      }
      return false;
    }
  });
}

export async function cancelCloudArchive(
  gid: string,
  options: { silent?: boolean; skipRefresh?: boolean } = {}
): Promise<boolean> {
  return withBusy(`download:cancel-archive:${gid}`, async () => {
    const snapshot = downloads.value;
    const prev = snapshot.find((item) => item.gid === gid);
    if (prev) {
      upsertDownloadRow({
        ...prev,
        saveTo: "local",
        cloudArchive: isCloudStored(prev) ? prev.cloudArchive : null
      });
    }
    try {
      const updated = await cancelDownloadArchive(gid);
      upsertDownloadRow(updated);
      if (!options.silent) {
        updateFeedback("已取消云盘转存，保留本地文件。", "info");
      }
      if (!options.skipRefresh && !sseConnected.value) {
        await refreshDownloads();
      }
      return true;
    } catch (error) {
      downloads.value = snapshot;
      if (!options.silent) {
        toastError(error, "取消云盘转存失败。");
      }
      return false;
    }
  });
}

function parseEpisodeNumber(title: string): number | null {
  const m =
    /第\s*0*(\d{1,4})\s*[话話集回]/u.exec(title) ??
    /\bS\d{1,2}\s*E0*(\d{1,4})\b/i.exec(title) ??
    /\b(?:EP?|Episode)\s*0*(\d{1,4})\b/i.exec(title) ??
    /(?:[\[【]\s*|\s[-–—]\s)0*(\d{1,4})(?:v\d+)?(?:\s*[\]】]|\s|$)/i.exec(title);
  if (!m) return null;
  const n = Number(m[1]);
  return Number.isFinite(n) && n >= 0 && n < 1900 ? n : null;
}

function sortGidsByEpisodeOrder(gids: string[]): string[] {
  const byGid = new Map(downloads.value.map((item) => [item.gid, item]));
  return [...gids].sort((a, b) => {
    const ja = byGid.get(a);
    const jb = byGid.get(b);
    const ea = ja ? parseEpisodeNumber(displayTitle(ja)) : null;
    const eb = jb ? parseEpisodeNumber(displayTitle(jb)) : null;
    if (ea !== null && eb !== null && ea !== eb) {
      return ea - eb;
    }
    if (ea !== null && eb === null) return -1;
    if (ea === null && eb !== null) return 1;
    return String(ja?.createdAt ?? "").localeCompare(String(jb?.createdAt ?? ""));
  });
}

export async function archiveBatchDownloads(gids: string[]): Promise<void> {
  const uniqueGids = sortGidsByEpisodeOrder(Array.from(new Set(gids)));
  if (!uniqueGids.length) {
    updateFeedback("没有可转存至云盘的任务。", "info");
    return;
  }
  let succeeded = 0;
  for (const gid of uniqueGids) {
    try {
      const ok = await triggerCloudArchive(gid, { silent: true, skipRefresh: true });
      if (ok) succeeded++;
    } catch {
      // counted in failed below
    }
  }
  const failed = uniqueGids.length - succeeded;
  await refreshDownloads().catch(() => undefined);
  if (failed > 0) {
    updateFeedback(
      succeeded > 0
        ? `已将 ${succeeded} 个任务加入云盘转存，${failed} 个失败（请检查 Google Drive 连接状态）。`
        : "云盘转存启动失败，请先在「设置 → 云盘归档」中连接 Google Drive。",
      succeeded > 0 ? "info" : "error"
    );
  } else {
    updateFeedback(`已将 ${succeeded} 个任务加入 Google Drive 转存队列。`, "success");
  }
}

export async function removeBatchDownloads(gids: string[], deleteLocalFiles: boolean): Promise<void> {
  const uniqueGids = Array.from(new Set(gids));
  if (!uniqueGids.length) {
    return;
  }
  const results = await Promise.allSettled(
    uniqueGids.map((gid) =>
      updateDownloadState("remove", gid, { deleteLocalFiles, silent: true, skipRefresh: true })
    )
  );
  const succeeded = results.filter((r) => r.status === "fulfilled" && r.value).length;
  const failed = uniqueGids.length - succeeded;
  await refreshDownloads().catch(() => undefined);
  if (failed > 0) {
    updateFeedback(`已删除 ${succeeded} 个任务，${failed} 个删除失败。`, "error");
  } else {
    updateFeedback(`已删除 ${succeeded} 个下载任务。`, "success");
  }
}

export async function handleDownloadConflict(gid: string, strategy: DownloadConflictStrategy): Promise<void> {
  await withBusy(`download:conflict:${strategy}:${gid}`, async () => {
    try {
      const updated = await resolveDownloadConflict(gid, strategy);
      upsertDownloadRow(updated);
      updateFeedback(
        strategy === "recheck"
          ? t("downloads.conflictRecheck")
          : strategy === "overwrite"
            ? t("downloads.conflictOverwrite")
            : t("downloads.conflictDeleteExisting"),
        "success"
      );
      await refreshDownloads();
    } catch (error) {
      toastError(error, t("downloads.conflictFailed"));
    }
  });
}

export async function clearFinishedDownloadRows(): Promise<void> {
  await withBusy("download:clear-finished", async () => {
    try {
      const result = await clearFinishedDownloads();
      await refreshDownloads();
      updateFeedback(t("downloads.clearFinishedDone", { count: result.removedCount }), "success");
    } catch (error) {
      toastError(error, t("downloads.clearFinishedFailed"));
    }
  });
}

async function runBatch(
  gids: string[],
  action: "pause" | "resume",
  messages: { none: string; done: (count: number) => string; partial: (count: number, failed: number) => string }
): Promise<void> {
  const deduped = Array.from(new Set(gids));
  const uniqueGids = action === "resume" ? sortGidsByEpisodeOrder(deduped) : deduped;
  if (!uniqueGids.length) {
    updateFeedback(messages.none, "info");
    return;
  }
  let failedCount = 0;
  if (action === "resume") {
    for (const gid of uniqueGids) {
      try {
        const ok = await updateDownloadState(action, gid, { silent: true, skipRefresh: true });
        if (!ok) failedCount++;
      } catch {
        failedCount++;
      }
    }
  } else {
    const results = await Promise.allSettled(
      uniqueGids.map((gid) => updateDownloadState(action, gid, { silent: true, skipRefresh: true }))
    );
    failedCount = results.filter((r) => r.status !== "fulfilled" || !r.value).length;
  }
  await refreshDownloads().catch(() => undefined);
  updateFeedback(
    failedCount ? messages.partial(uniqueGids.length, failedCount) : messages.done(uniqueGids.length),
    failedCount ? "error" : "success"
  );
}

export async function resumeAllDownloads(): Promise<void> {
  const gids = downloads.value.filter((item) => item.status === "paused").map((item) => item.gid);
  await runBatch(gids, "resume", {
    none: t("downloads.resumeAllNone"),
    done: (count) => t("downloads.resumeAllDone", { count }),
    partial: (count, failedCount) => t("downloads.resumeAllPartial", { count, failedCount })
  });
}

export async function pauseAllDownloads(): Promise<void> {
  const gids = downloads.value
    .filter((item) => ["active", "waiting", "queued"].includes(item.status))
    .map((item) => item.gid);
  await runBatch(gids, "pause", {
    none: t("downloads.pauseAllNone"),
    done: (count) => t("downloads.pauseAllDone", { count }),
    partial: (count, failedCount) => t("downloads.pauseAllPartial", { count, failedCount })
  });
}

export async function openPlayback(download: DownloadJob): Promise<void> {
  await withBusy(`download:playback:${download.gid}`, async () => {
    try {
      playbackRouteLabel.value = detailSearchKeyword(download) || displayTitle(download);
      activePlayback.value = await getDownloadPlaybackInfo(download.gid);
      syncLocationWithTab("push", { playbackGid: download.gid });
      const shouldLoadDetail = selectedDownloadDetailGid.value !== download.gid;
      selectedDownloadDetailGid.value = download.gid;
      if (shouldLoadDetail) {
        void openDownloadAnimeDetail(download);
      }
      if (!activePlayback.value.supportsInAppPlayback) {
        updateFeedback(t("playback.unsupportedInApp"), "info");
      }
    } catch (error) {
      toastError(error, t("playback.readFailed"));
    }
  });
}

export async function restorePlaybackFromLocation(activeTabKey: string): Promise<void> {
  const gid = readPlaybackGidFromLocation();
  if (activeTabKey !== "downloads" || !gid || activePlayback.value?.gid === gid) {
    return;
  }

  const download = downloads.value.find((item) => item.gid === gid);
  try {
    activePlayback.value = await getDownloadPlaybackInfo(gid);
    playbackRouteLabel.value = download
      ? detailSearchKeyword(download) || displayTitle(download)
      : activePlayback.value.fileName;
    selectedDownloadDetailGid.value = gid;
    if (download) {
      void openDownloadAnimeDetail(download);
    }
    if (!activePlayback.value.supportsInAppPlayback) {
      updateFeedback(t("playback.unsupportedInApp"), "info");
    }
  } catch (error) {
    toastError(error, t("playback.readFailed"));
  }
}

export function closePlayback(): void {
  activePlayback.value = null;
  playbackRouteLabel.value = "";
  syncLocationWithTab("replace", { playbackGid: null });
}

export async function launchExternalPlayer(gid: string, playerId: ExternalPlayerId): Promise<void> {
  await withBusy(`download:player:${gid}:${playerId}`, async () => {
    try {
      const result = await openDownloadPlayer(gid, playerId);
      updateFeedback(t("playback.playerCalled", { playerLabel: result.playerLabel }), "success");
    } catch (error) {
      toastError(error, t("playback.playerFailed"));
    }
  });
}
