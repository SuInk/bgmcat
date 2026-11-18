import type { DownloadJob } from "@shared/index";
import { formatSpeed } from "./format";

export interface CloudUploadProgress {
  uploaded: number;
  total: number;
  percent: number;
  speed: number;
  etaSeconds: number | null;
}

export function isCloudStored(download: DownloadJob): boolean {
  const status = download.cloudArchive?.status;
  return status === "verified" || status === "archived";
}

export function isCloudFailed(download: DownloadJob): boolean {
  return download.cloudArchive?.status === "error";
}

/** 正在上传 Google Drive，或下载刚完成、排队等待进入云盘上传槽位 */
export function isCloudTransferring(download: DownloadJob): boolean {
  if (download.cloudArchive?.status === "uploading") {
    return true;
  }
  return download.status === "complete" && download.saveTo === "cloud" && !download.cloudArchive;
}

/** 是否允许手动触发「转存云盘 / 立即重试」 */
export function canArchiveToCloud(download: DownloadJob): boolean {
  if (download.status === "removed") {
    return false;
  }
  if (isCloudStored(download)) {
    return false;
  }
  if (download.cloudArchive?.status === "uploading") {
    return false;
  }
  if (download.status !== "complete" && download.saveTo === "cloud" && !isCloudFailed(download)) {
    return false;
  }
  return true;
}

/** 是否允许取消转存（上传中、转存报错待重试、或已标记完成后转存但尚未归档） */
export function canCancelCloudArchive(download: DownloadJob): boolean {
  if (isCloudStored(download)) {
    return false;
  }
  return (
    download.cloudArchive?.status === "uploading" ||
    download.cloudArchive?.status === "error" ||
    download.saveTo === "cloud"
  );
}

export function cloudUploadProgress(download: DownloadJob): CloudUploadProgress {
  const files = download.files ?? [];
  const filesTotal = files.reduce((sum, file) => sum + (file.size ?? 0), 0);
  const total = filesTotal > 0 ? filesTotal : Math.max(0, download.totalLength);
  if (isCloudStored(download)) {
    return {
      uploaded: total,
      total,
      percent: 100,
      speed: 0,
      etaSeconds: null
    };
  }
  const uploaded = files.reduce(
    (sum, file) => sum + (file.verified ? (file.size ?? 0) : Math.min(file.uploadedBytes ?? 0, file.size ?? total)),
    0
  );
  const percent = total > 0 ? Math.max(0, Math.min(100, Math.floor((uploaded / total) * 100))) : 0;
  const speed = download.cloudArchive?.status === "uploading" ? Math.max(0, download.cloudArchive.uploadSpeed ?? 0) : 0;
  const remaining = Math.max(0, total - uploaded);
  const etaSeconds = speed > 0 && remaining > 0 ? Math.ceil(remaining / speed) : null;
  return { uploaded, total, percent, speed, etaSeconds };
}

export function cloudArchiveLabel(download: DownloadJob): string {
  const archive = download.cloudArchive;
  if (!archive) {
    if (download.status === "complete" && download.saveTo === "cloud") {
      return "等待转存云盘…";
    }
    return "";
  }
  switch (archive.status) {
    case "uploading":
      return uploadingLabel(download);
    case "verified":
      return "云端已校验 · 本地保留";
    case "archived":
      return "已存云盘 · 本地已清理";
    case "error":
      return "云盘转存失败 · 待重试";
  }
}

/** 上传中带上百分比与实时速率，一眼看出转存速度与是否在推进 */
function uploadingLabel(download: DownloadJob): string {
  const { total, percent, speed } = cloudUploadProgress(download);
  if (!total) {
    return speed > 0 ? `转存云盘中 · ${formatSpeed(speed)}` : "转存云盘中…";
  }
  if (speed > 0) {
    return `转存云盘 ${percent}% · ${formatSpeed(speed)}`;
  }
  return `转存云盘 ${percent}%`;
}
