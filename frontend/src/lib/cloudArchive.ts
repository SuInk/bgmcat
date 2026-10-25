import type { DownloadJob } from "@shared/index";

export function cloudArchiveLabel(download: DownloadJob): string {
  const archive = download.cloudArchive;
  if (!archive) return "";
  return {
    uploading: "上传云盘中",
    verified: "云端已校验 · 本地保留",
    archived: "已存云盘 · 本地已清理",
    error: "云盘归档待重试"
  }[archive.status];
}
