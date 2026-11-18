import { ref } from "vue";
import { extractEpisodeNumberFromTitle, extractSubgroup } from "@shared/episode";
import type { DownloadJob } from "@shared/index";
import { resolveDownloadArtworkUrl, type FileBrowserEntry } from "../api";
import { isCloudStored } from "./cloudArchive";
import { extractResolutionLabel } from "./mikan";
import { readStorageItem } from "./storageKeys";

/**
 * 播放进度与观看历史记忆，存在本机 localStorage：
 * 键是「文件路径或媒体ID:播放方式」，值是看到哪、总时长、是否看完及快照元数据。
 * 播放器负责写入；「观看历史」视图响应式订阅读取，支持下载任务与本地/云盘文件续播。
 */
export const PLAYBACK_HISTORY_KEY = "bgmcat.playback-history";
const LEGACY_PLAYBACK_HISTORY_KEY = "anime-rss-playback-history";
/** 不到这么多秒不弹续播浮层，离结尾不到这么多秒视为看完 */
export const RESUME_MIN_SECONDS = 8;
export const RESUME_END_GAP_SECONDS = 20;

export interface PlaybackHistoryFileSnapshot {
  id: string;
  name: string;
  displayName?: string;
  path: string;
  kind: FileBrowserEntry["kind"];
  size: number;
  modTime: string;
  ext: string;
  mediaId: string;
}

export interface PlaybackHistoryMeta {
  kind?: "download" | "file";
  title?: string;
  episodeLabel?: string | null;
  subgroup?: string | null;
  resolution?: string | null;
  imageUrl?: string | null;
  animeId?: string | null;
  folderLabel?: string | null;
  fileSource?: "local" | "cloud";
  fileEntry?: PlaybackHistoryFileSnapshot;
}

export interface PlaybackHistoryRecord extends PlaybackHistoryMeta {
  currentTime: number;
  duration: number;
  updatedAt: number;
  gid?: string;
  completed?: boolean;
}

export type PlaybackHistory = Record<string, PlaybackHistoryRecord>;

/** 每次写入/删除/清空观看历史时递增，驱动响应式计算属性即时刷新 */
export const playbackHistoryVersion = ref(0);

export function bumpPlaybackHistoryVersion(): void {
  playbackHistoryVersion.value += 1;
}

if (typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.key === PLAYBACK_HISTORY_KEY || event.key === null) {
      bumpPlaybackHistoryVersion();
    }
  });
}

export function readPlaybackHistory(): PlaybackHistory {
  try {
    const raw = readStorageItem(PLAYBACK_HISTORY_KEY, LEGACY_PLAYBACK_HISTORY_KEY);
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
  bumpPlaybackHistoryVersion();
}

export function removePlaybackHistoryKeys(keys: string[]): void {
  if (!keys.length) {
    return;
  }
  const history = readPlaybackHistory();
  let changed = false;
  for (const key of keys) {
    if (key in history) {
      delete history[key];
      changed = true;
    }
  }
  if (changed) {
    writePlaybackHistory(history);
  }
}

export function clearAllPlaybackHistory(): void {
  const history = readPlaybackHistory();
  const next: PlaybackHistory = {};
  let removed = false;
  for (const [key, value] of Object.entries(history)) {
    if (key.startsWith("share:")) {
      next[key] = value;
    } else {
      removed = true;
    }
  }
  if (removed) {
    writePlaybackHistory(next);
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
    .filter(([key]) => !key.startsWith("share:"))
    .map(([key, record]) => {
      const path = key.slice(0, key.lastIndexOf(":"));
      const download = (record.gid && byGid.get(record.gid)) || byPath.get(path);
      return download ? { download, record } : null;
    })
    .filter((entry): entry is ContinueWatchingEntry => {
      if (!entry || seen.has(entry.download.gid) || entry.record.completed) {
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

function decodeBase64UrlUtf8(encoded: string): string | null {
  try {
    const padded = encoded.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(encoded.length / 4) * 4, "=");
    const bin = window.atob(padded);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i += 1) {
      bytes[i] = bin.charCodeAt(i);
    }
    return new TextDecoder().decode(bytes);
  } catch {
    return null;
  }
}

function decodeLegacyFileEntry(mediaId: string): { source: "local" | "cloud"; entry: FileBrowserEntry; folderLabel: string } | null {
  if (mediaId.startsWith("local~")) {
    const rel = decodeBase64UrlUtf8(mediaId.slice("local~".length));
    if (!rel) return null;
    const parts = rel.split("/").filter(Boolean);
    const name = parts[parts.length - 1] ?? rel;
    const parent = parts.slice(0, -1).join(" / ");
    const dot = name.lastIndexOf(".");
    const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
    return {
      source: "local",
      folderLabel: parent ? `本地目录 / ${parent}` : "本地下载目录",
      entry: {
        id: rel,
        name,
        path: rel,
        isDir: false,
        kind: "video",
        size: 0,
        modTime: "",
        ext,
        playable: true,
        mediaId
      }
    };
  }
  if (mediaId.startsWith("drive~")) {
    const segs = mediaId.split("~");
    if (segs.length !== 3 || !segs[1] || !segs[2]) return null;
    const name = decodeBase64UrlUtf8(segs[2]);
    if (!name) return null;
    const dot = name.lastIndexOf(".");
    const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
    return {
      source: "cloud",
      folderLabel: "Google Drive 云盘",
      entry: {
        id: segs[1],
        name,
        path: segs[1],
        isDir: false,
        kind: "video",
        size: 0,
        modTime: "",
        ext,
        playable: true,
        mediaId
      }
    };
  }
  return null;
}

function stripFileExtension(name: string): string {
  return name.replace(/\.[a-z0-9]{2,5}$/iu, "").trim() || name;
}

function cleanAnimeTitleFromRelease(raw: string): string {
  const withoutExt = stripFileExtension(raw);
  const stripped = withoutExt
    .replace(/^\s*(?:\[[^\]]+\]|【[^】]+】)\s*/gu, "")
    .replace(/\s*(?:\[[^\]]*\]|【[^】]*】|\([^)]*\)|（[^）]*）)\s*/gu, " ")
    .replace(/(?:-\s*)?\b(?:S\d{1,2}E\d{1,3}|EP?\s*\d{1,3}|\d{1,3}\s*(?:话|集|話))\b.*$/iu, "")
    .split(/\s+\/\s+/u)[0]
    ?.trim();
  return stripped || withoutExt;
}

function formatEpisodeBadge(rawTitle: string): string | null {
  const ep = extractEpisodeNumberFromTitle(rawTitle);
  return ep ? `第 ${ep} 集` : null;
}

export type WatchHistorySourceKind = "download" | "cloud-download" | "local-file" | "cloud-file" | "removed";

export interface WatchHistoryItem {
  id: string;
  keys: string[];
  title: string;
  rawTitle: string;
  episodeLabel: string | null;
  subgroup: string | null;
  resolution: string | null;
  imageUrl: string | null;
  animeId: string | null;
  sourceKind: WatchHistorySourceKind;
  sourceLabel: string;
  folderLabel: string | null;
  currentTime: number;
  duration: number;
  progressPercent: number;
  completed: boolean;
  updatedAt: number;
  playable: boolean;
  download: DownloadJob | null;
  filePlayback: {
    source: "local" | "cloud";
    entry: FileBrowserEntry;
    folderLabel: string;
  } | null;
}

export interface WatchHistoryResolveHelpers {
  shortTitle?: (download: DownloadJob) => string;
  fullTitle?: (download: DownloadJob) => string;
}

export function watchHistoryEntries(
  downloads: DownloadJob[],
  helpers: WatchHistoryResolveHelpers = {},
  history = readPlaybackHistory()
): WatchHistoryItem[] {
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

  const sortedRaw = Object.entries(history)
    .filter(([key, record]) => {
      if (key.startsWith("share:") || !record || typeof record !== "object") {
        return false;
      }
      const { currentTime, duration } = record;
      return (
        Number.isFinite(currentTime) &&
        Number.isFinite(duration) &&
        duration > 0 &&
        (Boolean(record.completed) || currentTime >= 1)
      );
    })
    .sort((left, right) => (right[1].updatedAt || 0) - (left[1].updatedAt || 0));

  const byDedupeId = new Map<string, WatchHistoryItem>();

  for (const [key, record] of sortedRaw) {
    const colonIdx = key.lastIndexOf(":");
    const rawTarget = colonIdx > 0 ? key.slice(0, colonIdx) : key;
    const download = (record.gid && byGid.get(record.gid)) || byPath.get(rawTarget) || null;
    const isCompleted =
      Boolean(record.completed) ||
      (record.duration - record.currentTime <= RESUME_END_GAP_SECONDS &&
        record.currentTime >= Math.min(RESUME_MIN_SECONDS, record.duration * 0.5));
    const clampedCurrent = isCompleted ? record.duration : Math.min(record.currentTime, record.duration);
    const pct = isCompleted
      ? 100
      : Math.max(2, Math.min(99, Math.round((clampedCurrent / record.duration) * 100)));

    if (download) {
      const dedupeId = `dl:${download.gid}`;
      const existing = byDedupeId.get(dedupeId);
      if (existing) {
        existing.keys.push(key);
        if (!existing.imageUrl && record.imageUrl) existing.imageUrl = record.imageUrl;
        if (!existing.animeId && record.animeId) existing.animeId = record.animeId;
        continue;
      }

      const rawRelease = helpers.fullTitle ? helpers.fullTitle(download) : download.feedItemTitle || rawTarget;
      const baseTitle =
        record.title?.trim() ||
        (helpers.shortTitle ? helpers.shortTitle(download) : cleanAnimeTitleFromRelease(rawRelease));
      const guid = download.feedItemGuid?.trim() ?? "";
      const savedAnimeId = record.animeId || (guid.startsWith("anime:") ? guid.slice("anime:".length).trim() : null);
      const cloudArchived = isCloudStored(download);

      byDedupeId.set(dedupeId, {
        id: dedupeId,
        keys: [key],
        title: baseTitle,
        rawTitle: rawRelease,
        episodeLabel: record.episodeLabel ?? formatEpisodeBadge(download.feedItemTitle || rawRelease),
        subgroup: record.subgroup ?? extractSubgroup(download.feedItemTitle || rawRelease),
        resolution: record.resolution ?? extractResolutionLabel(download.feedItemTitle || rawRelease),
        imageUrl: resolveDownloadArtworkUrl(download) ?? record.imageUrl ?? null,
        animeId: savedAnimeId || null,
        sourceKind: cloudArchived ? "cloud-download" : "download",
        sourceLabel: cloudArchived ? "云盘归档" : "下载任务",
        folderLabel: null,
        currentTime: clampedCurrent,
        duration: record.duration,
        progressPercent: pct,
        completed: isCompleted,
        updatedAt: record.updatedAt || 0,
        playable: true,
        download,
        filePlayback: null
      });
      continue;
    }

    const mediaCandidate = record.fileEntry?.mediaId || record.gid || rawTarget;
    const snap = record.fileEntry;
    const decodedLegacy = !snap ? decodeLegacyFileEntry(mediaCandidate) : null;
    if (snap || decodedLegacy) {
      const source = record.fileSource ?? decodedLegacy?.source ?? (mediaCandidate.startsWith("drive~") ? "cloud" : "local");
      const entry: FileBrowserEntry = snap
        ? {
            id: snap.id,
            name: snap.name,
            displayName: snap.displayName,
            path: snap.path,
            isDir: false,
            kind: snap.kind,
            size: snap.size,
            modTime: snap.modTime,
            ext: snap.ext,
            playable: true,
            mediaId: snap.mediaId
          }
        : decodedLegacy!.entry;
      const dedupeId = `file:${entry.mediaId}`;
      const existing = byDedupeId.get(dedupeId);
      if (existing) {
        existing.keys.push(key);
        continue;
      }

      const rawFileName = entry.displayName || entry.name;
      const folderLabel = record.folderLabel ?? decodedLegacy?.folderLabel ?? (source === "cloud" ? "Google Drive 云盘" : "本地下载目录");
      byDedupeId.set(dedupeId, {
        id: dedupeId,
        keys: [key],
        title: record.title?.trim() || cleanAnimeTitleFromRelease(rawFileName),
        rawTitle: rawFileName,
        episodeLabel: record.episodeLabel ?? formatEpisodeBadge(rawFileName),
        subgroup: record.subgroup ?? extractSubgroup(rawFileName),
        resolution: record.resolution ?? extractResolutionLabel(rawFileName),
        imageUrl: record.imageUrl ?? null,
        animeId: record.animeId ?? null,
        sourceKind: source === "cloud" ? "cloud-file" : "local-file",
        sourceLabel: source === "cloud" ? "云盘文件" : "本地文件",
        folderLabel,
        currentTime: clampedCurrent,
        duration: record.duration,
        progressPercent: pct,
        completed: isCompleted,
        updatedAt: record.updatedAt || 0,
        playable: true,
        download: null,
        filePlayback: {
          source,
          entry,
          folderLabel
        }
      });
      continue;
    }

    // 任务已被清理但本机仍保留的历史记录
    const fallbackName = rawTarget.split(/[\\/]/).filter(Boolean).pop() || "未知视频";
    const dedupeId = `orphan:${record.gid || rawTarget}`;
    const existing = byDedupeId.get(dedupeId);
    if (existing) {
      existing.keys.push(key);
      continue;
    }

    byDedupeId.set(dedupeId, {
      id: dedupeId,
      keys: [key],
      title: record.title?.trim() || cleanAnimeTitleFromRelease(fallbackName),
      rawTitle: fallbackName,
      episodeLabel: record.episodeLabel ?? formatEpisodeBadge(fallbackName),
      subgroup: record.subgroup ?? extractSubgroup(fallbackName),
      resolution: record.resolution ?? extractResolutionLabel(fallbackName),
      imageUrl: record.imageUrl ?? null,
      animeId: record.animeId ?? null,
      sourceKind: "removed",
      sourceLabel: "任务已清理",
      folderLabel: null,
      currentTime: clampedCurrent,
      duration: record.duration,
      progressPercent: pct,
      completed: isCompleted,
      updatedAt: record.updatedAt || 0,
      playable: false,
      download: null,
      filePlayback: null
    });
  }

  return Array.from(byDedupeId.values()).sort((a, b) => b.updatedAt - a.updatedAt);
}
