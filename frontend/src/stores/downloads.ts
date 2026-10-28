import { computed, reactive, ref } from "vue";

import type {
  DownloadConflictStrategy,
  DownloadJob,
  DownloadPlaybackInfo,
  ExternalPlayerId,
  SourceType
} from "@shared/index";

import {
  clearFinishedDownloads,
  createDownload,
  getDownloadPlaybackInfo,
  listDownloads,
  openDownloadPlayer,
  pauseDownload,
  removeDownload,
  resolveDownloadConflict,
  resumeDownload
} from "../api";
import { t } from "../i18n";
import { downloadDetailSearchKeyword, downloadDisplayTitle } from "../lib/downloadTitle";
import { isBusyMatching, withBusy } from "./busy";
import { openDownloadAnimeDetail } from "./discovery";
import { playbackRouteLabel, readPlaybackGidFromLocation, syncLocationWithTab } from "./router";
import { feedItemById, refreshFeedItems } from "./subscriptions";
import { toastError, updateFeedback } from "./toasts";

export const downloads = ref<DownloadJob[]>([]);
export const selectedDownloadDetailGid = ref<string | null>(null);
export const activePlayback = ref<DownloadPlaybackInfo | null>(null);

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

export const inferredManualSourceType = computed<SourceType>(() => {
  const value = manualDownloadForm.url.trim().toLowerCase();
  if (value.startsWith("magnet:")) {
    return "magnet";
  }
  if (value.endsWith(".torrent")) {
    return "torrent-url";
  }
  return "direct";
});

export const onlineDownloadCount = computed(
  () => downloads.value.filter((item) => item.status === "active" || item.status === "waiting").length
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
      key === `download:playback:${gid}` ||
      key.startsWith(`download:player:${gid}:`) ||
      key.startsWith(`download:conflict:`) && key.endsWith(`:${gid}`)
  );
}

/**
 * 已完成但用户还没去看过的下载数量，用于侧栏「下载管理」未读角标。
 * 后端没有推送通道，完成事件靠 refreshDownloads 前后对比得出。
 */
export const unreadCompletedDownloadCount = ref(0);

export function clearUnreadCompletedDownloads(): void {
  unreadCompletedDownloadCount.value = 0;
}

export async function refreshDownloads(): Promise<void> {
  const previousStatusByGid = new Map(downloads.value.map((item) => [item.gid, item.status]));
  const next = await listDownloads();
  downloads.value = next;

  // 首次加载（previous 为空）不算完成事件，否则每次刷新页面都会炸一堆 toast。
  if (!previousStatusByGid.size) {
    return;
  }

  const justCompleted = next.filter(
    (item) => item.status === "complete" && previousStatusByGid.get(item.gid) !== undefined
      && previousStatusByGid.get(item.gid) !== "complete"
  );
  if (!justCompleted.length) {
    return;
  }

  unreadCompletedDownloadCount.value += justCompleted.length;
  if (justCompleted.length === 1) {
    updateFeedback(`下载完成：${displayTitle(justCompleted[0]!)}`, "success");
  } else {
    updateFeedback(`${justCompleted.length} 个下载已完成。`, "success");
  }
}

export async function submitManualDownload(): Promise<void> {
  const url = manualDownloadForm.url.trim();
  if (!url) {
    updateFeedback("请先填写下载链接。", "error");
    return;
  }
  await withBusy("download:manual", async () => {
    try {
      await createDownload({
        url,
        sourceType: manualDownloadForm.sourceTypeOverride ?? inferredManualSourceType.value,
        saveTo: manualDownloadForm.saveTo,
        ...(manualDownloadForm.saveTo === "local" && manualDownloadForm.directory.trim()
          ? { directory: manualDownloadForm.directory.trim() }
          : {})
      });
      manualDownloadForm.url = "";
      manualDownloadForm.sourceTypeOverride = null;
      updateFeedback(t("downloads.manualQueued"), "success");
      await Promise.allSettled([refreshDownloads(), refreshFeedItems()]);
    } catch (error) {
      toastError(error, t("downloads.manualFailed"));
    }
  });
}

export async function updateDownloadState(
  action: "pause" | "resume" | "remove",
  gid: string,
  options: { deleteLocalFiles?: boolean; silent?: boolean } = {}
): Promise<boolean> {
  return withBusy(`download:${action}:${gid}`, async () => {
    try {
      if (action === "pause") {
        await pauseDownload(gid);
        if (!options.silent) {
          updateFeedback(t("downloads.pauseCompleted"), "success");
        }
      } else if (action === "resume") {
        await resumeDownload(gid);
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
      await refreshDownloads();
      return true;
    } catch (error) {
      if (!options.silent) {
        toastError(error, t("downloads.updateFailed"));
      }
      return false;
    }
  });
}

export async function handleDownloadConflict(gid: string, strategy: DownloadConflictStrategy): Promise<void> {
  await withBusy(`download:conflict:${strategy}:${gid}`, async () => {
    try {
      await resolveDownloadConflict(gid, strategy);
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
  if (!gids.length) {
    updateFeedback(messages.none, "info");
    return;
  }
  let failedCount = 0;
  for (const gid of gids) {
    const succeeded = await updateDownloadState(action, gid, { silent: true });
    if (!succeeded) {
      failedCount += 1;
    }
  }
  updateFeedback(
    failedCount ? messages.partial(gids.length, failedCount) : messages.done(gids.length),
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
