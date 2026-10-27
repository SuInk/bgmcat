import type { DownloadJob } from "@shared/index";

export function cloudArchiveLabel(download: DownloadJob): string {
  const archive = download.cloudArchive;
  if (!archive) return "";
  return {
    uploading: uploadingLabel(download),
    verified: "云端已校验 · 本地保留",
    archived: "已存云盘 · 本地已清理",
    error: "云盘归档待重试"
  }[archive.status];
}

/** 上传中带上百分比：一集几百 MB，慢网要传好几分钟，光写「上传中」看不出有没有在动 */
function uploadingLabel(download: DownloadJob): string {
  const files = download.files ?? [];
  const total = files.reduce((sum, file) => sum + (file.size ?? 0), 0);
  if (!total) return "上传云盘中";
  const done = files.reduce((sum, file) => sum + (file.verified ? (file.size ?? 0) : Math.min(file.uploadedBytes ?? 0, file.size ?? 0)), 0);
  return `上传云盘中 ${Math.floor((done / total) * 100)}%`;
}
