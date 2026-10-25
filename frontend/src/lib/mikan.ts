import type { AnimeDetail, MikanDownloadItem } from "@shared/index";

import type { MikanDownloadGroup } from "../components/discovery/AnimeDetailPane.vue";
import { extractEpisodeCandidates } from "@shared/episode";

/**
 * 一条资源的展示来源。老的 MikanDownloadItem 没有 providerId 字段
 * （比如从缓存里读出来的旧数据），这种一律当蜜柑。
 */
export function providerIdOf(item: MikanDownloadItem): string | null {
  return (item as { providerId?: string }).providerId ?? null;
}

/** 提供了这条资源的全部来源（跨源合并后可能有多个）。 */
export function providerIdsOf(item: MikanDownloadItem): string[] {
  const merged = (item as { providerIds?: string[] }).providerIds;
  if (merged?.length) {
    return merged;
  }
  const single = providerIdOf(item);
  return single ? [single] : [];
}

export function extractResolutionLabel(title: string): string | null {
  const resolutionMatch = title.match(/\b(2160p|1440p|1080p|720p|480p)\b/i);
  if (resolutionMatch?.[1]) {
    return resolutionMatch[1].toUpperCase();
  }
  const dimensionMatch = title.match(/\b(?:3840|2560|1920|1280|854|720|640)\s*[x×]\s*(2160|1440|1080|720|480)\b/i);
  if (dimensionMatch?.[1]) {
    return `${dimensionMatch[1]}P`;
  }
  if (/\b4k\b/i.test(title)) {
    return "2160P";
  }
  return null;
}

export function buildMikanCountOptions(
  items: MikanDownloadItem[],
  getKey: (item: MikanDownloadItem) => string,
  getLabel: (key: string) => string
): Array<{ key: string; label: string; count: number }> {
  const counts = new Map<string, number>();
  for (const item of items) {
    const key = getKey(item);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }

  return Array.from(counts.entries())
    .map(([key, count]) => ({ key, label: getLabel(key), count }))
    .sort((left, right) => right.count - left.count || left.label.localeCompare(right.label, "zh-Hans-CN"));
}

function normalizeEpisodeNumber(value: string | null | undefined): number | null {
  if (!value) {
    return null;
  }
  const numeric = Number.parseInt(value, 10);
  return Number.isFinite(numeric) && numeric > 0 ? numeric : null;
}

function normalizeEpisodeTitle(value: string): string {
  return value
    .toLocaleLowerCase()
    .replace(/[第\s\-_:：・·.。/\\[\]【】（）()'"“”‘’]+/g, "")
    .replace(/[集话話]/g, "");
}

function extractEpisodeNumberCandidate(title: string, availableNumbers: Set<number>): number | null {
  // 与下载分组共用同一套解析规则，再用番剧真实集数表约束，避免误配
  for (const candidate of extractEpisodeCandidates(title)) {
    if (availableNumbers.has(candidate)) {
      return candidate;
    }
  }
  return null;
}

function findEpisodeForDownload(
  item: MikanDownloadItem,
  episodes: AnimeDetail["episodes"]
): AnimeDetail["episodes"][number] | null {
  const episodesByNumber = new Map<number, AnimeDetail["episodes"][number]>();
  for (const episode of episodes) {
    const number = normalizeEpisodeNumber(episode.episodeNumber);
    if (number !== null) {
      episodesByNumber.set(number, episode);
    }
  }

  const numberCandidate = extractEpisodeNumberCandidate(item.title, new Set(episodesByNumber.keys()));
  if (numberCandidate !== null) {
    return episodesByNumber.get(numberCandidate) ?? null;
  }

  const normalizedTitle = normalizeEpisodeTitle(item.title);
  return (
    episodes.find((episode) => {
      const title = normalizeEpisodeTitle(episode.title);
      return Boolean(title && title.length >= 4 && !/^\d+$/.test(title) && normalizedTitle.includes(title));
    }) ?? null
  );
}

export function groupMikanDownloadsByEpisode(
  items: MikanDownloadItem[],
  episodes: AnimeDetail["episodes"]
): MikanDownloadGroup[] {
  if (!items.length) {
    return [];
  }

  const groups = new Map<string, MikanDownloadGroup>();
  for (const item of items) {
    const episode = findEpisodeForDownload(item, episodes);
    const episodeNumber = normalizeEpisodeNumber(episode?.episodeNumber);
    const key = episode ? `episode:${episode.episodeId}` : "unmatched";
    const label = episode
      ? `${episodeNumber ? `第 ${episodeNumber} 集` : "剧集"} · ${episode.title}`
      : "未匹配剧集";
    const group = groups.get(key) ?? {
      key,
      label,
      detail: episode?.airDate ?? null,
      order: episodeNumber ?? Number.MAX_SAFE_INTEGER - 1,
      downloads: []
    };
    group.downloads.push(item);
    groups.set(key, group);
  }

  return Array.from(groups.values()).sort((left, right) => {
    if (left.key === "unmatched") {
      return 1;
    }
    if (right.key === "unmatched") {
      return -1;
    }
    return left.order - right.order;
  });
}
