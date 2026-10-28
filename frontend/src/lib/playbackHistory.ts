import type { DownloadJob } from "@shared/index";

/**
 * 播放进度记忆，存在本机 localStorage：键是「文件路径:播放方式」，值是看到哪、总时长、何时更新。
 * 播放器负责写；首页「继续观看」只读，用 gid（新记录才有）或文件路径把记录对回下载任务。
 */
export const PLAYBACK_HISTORY_KEY = "anime-rss-playback-history";
/** 不到这么多秒不算看过，离结尾不到这么多秒算看完 */
export const RESUME_MIN_SECONDS = 8;
export const RESUME_END_GAP_SECONDS = 20;

export interface PlaybackHistoryRecord {
  currentTime: number;
  duration: number;
  updatedAt: number;
  gid?: string;
}

export type PlaybackHistory = Record<string, PlaybackHistoryRecord>;

export function readPlaybackHistory(): PlaybackHistory {
  try {
    const raw = window.localStorage.getItem(PLAYBACK_HISTORY_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw) as PlaybackHistory;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

export function writePlaybackHistory(nextHistory: PlaybackHistory): void {
  try {
    window.localStorage.setItem(PLAYBACK_HISTORY_KEY, JSON.stringify(nextHistory));
  } catch {
    // 隐私模式下写不进去就算了，不该因此中断播放
  }
}

export interface ContinueWatchingEntry {
  download: DownloadJob;
  record: PlaybackHistoryRecord;
}

/** 看了一部分、还没看完、对应任务还在的记录，最近看的排前面，同一任务只留一条 */
export function continueWatchingEntries(downloads: DownloadJob[], history = readPlaybackHistory()): ContinueWatchingEntry[] {
  const byGid = new Map(downloads.map((download) => [download.gid, download]));
  const byPath = new Map<string, DownloadJob>();
  for (const download of downloads) {
    if (download.filePath) {
      byPath.set(download.filePath, download);
    }
    for (const file of download.files ?? []) {
      if (file.path) {
        byPath.set(file.path, download);
      }
    }
  }
  const seen = new Set<string>();
  return Object.entries(history)
    .map(([key, record]) => {
      const path = key.slice(0, key.lastIndexOf(":"));
      const download = (record.gid && byGid.get(record.gid)) || byPath.get(path);
      return download ? { download, record } : null;
    })
    .filter((entry): entry is ContinueWatchingEntry => {
      if (!entry || seen.has(entry.download.gid)) {
        return false;
      }
      const { currentTime, duration } = entry.record;
      const ok =
        Number.isFinite(currentTime) &&
        Number.isFinite(duration) &&
        currentTime >= RESUME_MIN_SECONDS &&
        duration - currentTime > RESUME_END_GAP_SECONDS;
      if (ok) {
        seen.add(entry.download.gid);
      }
      return ok;
    })
    .sort((left, right) => right.record.updatedAt - left.record.updatedAt);
}
