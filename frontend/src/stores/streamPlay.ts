import { ref, watch } from "vue";

import type { DownloadJob, MikanDownloadItem } from "@shared/index";

import { streamDownloadNow } from "../api";
import { canPlayDownload } from "../lib/downloadGrouping";
import { selectTab } from "./boot";
import { findQueuedMikanDownload, queueMikanDownload } from "./discovery";
import { downloads, openPlayback } from "./downloads";
import { activeTab } from "./router";
import { toastError, updateFeedback } from "./toasts";

/** 正在等待开头片段缓冲完成、就绪后自动打开播放页的任务。 */
export const pendingStreamGid = ref<string | null>(null);
/** 正在发起边下边播的资源（用于按钮的「准备中…」状态）。 */
export const streamingMikanId = ref<string | null>(null);

const STREAM_WAIT_MS = 3 * 60 * 1000;
let waitTimer: ReturnType<typeof setTimeout> | null = null;

function clearPending(): void {
  pendingStreamGid.value = null;
  if (waitTimer) {
    clearTimeout(waitTimer);
    waitTimer = null;
  }
}

function playNow(job: DownloadJob): void {
  clearPending();
  if (activeTab.value !== "downloads") {
    selectTab("downloads");
  }
  void openPlayback(job);
}

export function isStreamingMikanItem(item: MikanDownloadItem): boolean {
  const job = findQueuedMikanDownload(item);
  return streamingMikanId.value === item.id || Boolean(job && pendingStreamGid.value === job.gid);
}

export function streamLabelOf(item: MikanDownloadItem): string {
  if (isStreamingMikanItem(item)) {
    return "缓冲中…";
  }
  const job = findQueuedMikanDownload(item);
  return job?.status === "complete" ? "播放" : "边下边播";
}

/**
 * 边下边播：资源没加入就先加入下载，然后让任务插队立即开始、优先拉媒体开头的片段，
 * 一旦第一个片段校验通过就打开播放页；播放器拖动进度时后端会把对应片段提到最高优先级。
 */
export async function streamMikanDownload(item: MikanDownloadItem): Promise<void> {
  if (streamingMikanId.value) return;
  streamingMikanId.value = item.id;
  try {
    const job = findQueuedMikanDownload(item) ?? (await queueMikanDownload(item));
    if (!job) return;
    if (canPlayDownload(job)) {
      playNow(job);
      return;
    }
    try {
      await streamDownloadNow(job.gid);
    } catch (error) {
      toastError(error, "无法边下边播");
      return;
    }
    clearPending();
    pendingStreamGid.value = job.gid;
    waitTimer = setTimeout(() => {
      if (pendingStreamGid.value === job.gid) {
        clearPending();
        updateFeedback("暂时没有连上做种用户，开头片段还没缓冲好；任务会继续下载，可稍后在下载管理里播放。", "info");
      }
    }, STREAM_WAIT_MS);
    updateFeedback("正在连接做种用户并缓冲开头片段，就绪后自动开始播放…", "info", {
      label: "取消",
      onClick: clearPending
    });
    const latest = downloads.value.find((d) => d.gid === job.gid);
    if (latest && canPlayDownload(latest)) {
      playNow(latest);
    }
  } finally {
    streamingMikanId.value = null;
  }
}

watch(downloads, (list) => {
  const gid = pendingStreamGid.value;
  if (!gid) return;
  const job = list.find((d) => d.gid === gid);
  if (!job) {
    clearPending();
    return;
  }
  if (canPlayDownload(job)) {
    playNow(job);
  } else if (job.status === "error") {
    clearPending();
    updateFeedback(`边下边播失败：${job.errorMessage ?? "下载出错"}`, "error");
  }
});
