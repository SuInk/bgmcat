import { computed, ref } from "vue";

import type { DownloadJob } from "@shared/index";

import {
  clearExpiredShares,
  createShare,
  deleteShare,
  listShares,
  updateShare,
  type FileBrowserEntry,
  type ShareRecord,
  type ShareTargetInput
} from "../api";
import { canPlayDownload, playableDownloadForEpisode, type SeriesDownloadGroup } from "../lib/downloadGrouping";
import { displayTitle } from "./downloads";
import { status } from "./session";
import { toastError, updateFeedback } from "./toasts";

export interface ShareDraft {
  singleTitle: string;
  singleTargets: ShareTargetInput[];
  singleSubLabel?: string;
  seriesTitle?: string;
  seriesTargets?: ShareTargetInput[];
  defaultScope: "single" | "series";
}

export interface ShareOriginOption {
  key: string;
  label: string;
  origin: string;
  isLoopback: boolean;
}

export const shares = ref<ShareRecord[]>([]);
export const sharesLoading = ref(false);
export const shareModalOpen = ref(false);
export const shareModalTab = ref<"create" | "manage">("create");
export const shareDraft = ref<ShareDraft | null>(null);
export const selectedOriginOverride = ref<string>("");

function normalizeOrigin(raw: string | null | undefined): string {
  if (!raw) return "";
  const trimmed = raw.trim().replace(/\/+$/u, "");
  if (!/^https?:\/\//iu.test(trimmed)) return "";
  try {
    const parsed = new URL(trimmed);
    return parsed.origin;
  } catch {
    return "";
  }
}

function isLoopbackHost(origin: string): boolean {
  try {
    const host = new URL(origin).hostname.toLowerCase();
    return host === "127.0.0.1" || host === "localhost" || host === "::1" || host === "[::1]";
  } catch {
    return true;
  }
}

export const availableShareOrigins = computed<ShareOriginOption[]>(() => {
  const seen = new Set<string>();
  const options: ShareOriginOption[] = [];

  const pushOrigin = (key: string, label: string, raw: string | null | undefined) => {
    const origin = normalizeOrigin(raw);
    if (!origin || seen.has(origin)) return;
    seen.add(origin);
    options.push({
      key,
      label,
      origin,
      isLoopback: isLoopbackHost(origin)
    });
  };

  if (status.value?.tunnelPublicUrl) {
    pushOrigin("tunnel", "公网穿透", status.value.tunnelPublicUrl);
  }

  const winOrigin = typeof window !== "undefined" ? normalizeOrigin(window.location.origin) : "";
  if (winOrigin && !isLoopbackHost(winOrigin)) {
    pushOrigin("current", "当前地址", winOrigin);
  }

  const lanUrls = status.value?.lanUrls ?? [];
  lanUrls.forEach((url, index) => {
    pushOrigin(`lan:${index}`, index === 0 ? "局域网" : `局域网 ${index + 1}`, url);
  });

  if (winOrigin) {
    pushOrigin("local", "本机", winOrigin);
  }

  return options;
});

export const activeShareOrigin = computed<ShareOriginOption | null>(() => {
  const list = availableShareOrigins.value;
  if (!list.length) return null;
  if (selectedOriginOverride.value) {
    const matched = list.find((item) => item.origin === selectedOriginOverride.value);
    if (matched) return matched;
  }
  return list[0] ?? null;
});

export const activeSharesCount = computed(() => shares.value.filter((item) => !item.expired).length);
export const expiredSharesCount = computed(() => shares.value.filter((item) => item.expired).length);

export function buildShareUrl(code: string, baseOrigin?: string): string {
  const origin =
    normalizeOrigin(baseOrigin) ||
    activeShareOrigin.value?.origin ||
    (typeof window !== "undefined" ? window.location.origin.replace(/\/+$/u, "") : "");
  return `${origin}/s/${encodeURIComponent(code)}`;
}

export function formatShareCopyText(
  record: Pick<ShareRecord, "title" | "passcode" | "hasPasscode">,
  shareUrl: string
): string {
  const title = record.title.trim();
  const passcode = record.passcode?.trim();
  if (passcode) {
    return `${title ? `「${title}」\n` : ""}链接：${shareUrl}\n提取码：${passcode}`;
  }
  return title ? `「${title}」 ${shareUrl}` : shareUrl;
}

export async function copyTextToClipboard(text: string, successMessage = "已复制到剪贴板"): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand("copy");
      document.body.removeChild(textarea);
    }
    if (successMessage) {
      updateFeedback(successMessage, "success");
    }
    return true;
  } catch {
    updateFeedback("复制失败，请手动选中链接复制", "error");
    return false;
  }
}

export async function refreshShares(silent = true): Promise<void> {
  sharesLoading.value = true;
  try {
    shares.value = await listShares();
  } catch (error) {
    if (!silent) {
      toastError(error, "读取分享列表失败");
    }
  } finally {
    sharesLoading.value = false;
  }
}

function collectGroupTargets(group: SeriesDownloadGroup): ShareTargetInput[] {
  const targets: ShareTargetInput[] = [];
  const seenGids = new Set<string>();
  for (const ep of group.episodes) {
    const candidate = playableDownloadForEpisode(ep) ?? ep.downloads.find((d) => d.status === "complete") ?? null;
    if (!candidate || seenGids.has(candidate.gid)) continue;
    seenGids.add(candidate.gid);
    targets.push({
      kind: "download",
      gid: candidate.gid,
      label: ep.episodeLabel && ep.episodeLabel !== "未知集数" ? ep.episodeLabel : undefined
    });
  }
  if (!targets.length) {
    for (const d of group.downloads) {
      if ((canPlayDownload(d) || d.status === "complete") && !seenGids.has(d.gid)) {
        seenGids.add(d.gid);
        targets.push({ kind: "download", gid: d.gid });
      }
    }
  }
  return targets;
}

export function canShareSeriesGroup(group: SeriesDownloadGroup): boolean {
  return group.downloads.some((d) => canPlayDownload(d) || d.status === "complete");
}

export function openShareManager(): void {
  shareModalTab.value = "manage";
  shareModalOpen.value = true;
  void refreshShares(false);
}

export function openShareForSeriesGroup(group: SeriesDownloadGroup): void {
  const groupTargets = collectGroupTargets(group);
  if (!groupTargets.length) {
    updateFeedback("该番剧暂无已就绪的可分享集数", "info");
    return;
  }
  const primaryTarget = group.playable
    ? {
        kind: "download" as const,
        gid: group.playable.gid,
        label: group.playableEpisodeLabel ?? undefined
      }
    : groupTargets[groupTargets.length - 1]!;

  const singleTitle = group.playableEpisodeLabel
    ? `${group.title} ${group.playableEpisodeLabel}`
    : group.title;

  shareDraft.value = {
    singleTitle,
    singleTargets: [primaryTarget],
    singleSubLabel: group.playableEpisodeLabel ?? "单集",
    seriesTitle: groupTargets.length > 1 ? group.title : undefined,
    seriesTargets: groupTargets.length > 1 ? groupTargets : undefined,
    defaultScope: groupTargets.length > 1 ? "series" : "single"
  };
  shareModalTab.value = "create";
  shareModalOpen.value = true;
  void refreshShares(true);
}

export function openShareForDownload(
  download: DownloadJob,
  group?: SeriesDownloadGroup | null,
  episodeLabel?: string | null
): void {
  const rawTitle = displayTitle(download);
  const cleanEp = episodeLabel && episodeLabel !== "未知集数" ? episodeLabel : null;
  const singleTitle = group?.title
    ? cleanEp
      ? `${group.title} ${cleanEp}`
      : group.title
    : rawTitle;
  const groupTargets = group ? collectGroupTargets(group) : [];

  shareDraft.value = {
    singleTitle,
    singleTargets: [
      {
        kind: "download",
        gid: download.gid,
        label: cleanEp ?? undefined
      }
    ],
    singleSubLabel: cleanEp ?? "当前单集",
    seriesTitle: group && groupTargets.length > 1 ? group.title : undefined,
    seriesTargets: group && groupTargets.length > 1 ? groupTargets : undefined,
    defaultScope: "single"
  };
  shareModalTab.value = "create";
  shareModalOpen.value = true;
  void refreshShares(true);
}

export function openShareForFileEntry(entry: FileBrowserEntry, source: "local" | "cloud"): void {
  const title = (entry.displayName || entry.name).replace(/\.[a-z0-9]{1,6}$/iu, "") || entry.name;
  const target: ShareTargetInput =
    source === "local"
      ? { kind: "local", path: entry.path }
      : {
          kind: "cloud",
          id: entry.id,
          name: entry.name,
          mimeType: entry.mimeType,
          size: entry.size,
          isDir: entry.isDir
        };

  shareDraft.value = {
    singleTitle: title,
    singleTargets: [target],
    singleSubLabel: entry.isDir ? "文件夹" : "文件",
    defaultScope: "single"
  };
  shareModalTab.value = "create";
  shareModalOpen.value = true;
  void refreshShares(true);
}

export function closeShareModal(): void {
  shareModalOpen.value = false;
}

export async function submitCreateShare(payload: {
  title: string;
  expiresInHours: number;
  passcode: string;
  allowDownload: boolean;
  targets: ShareTargetInput[];
}): Promise<ShareRecord | null> {
  try {
    const created = await createShare(payload);
    shares.value = [created, ...shares.value.filter((item) => item.code !== created.code)];
    return created;
  } catch (error) {
    toastError(error, "创建分享链接失败");
    return null;
  }
}

export async function toggleShareAllowDownload(record: ShareRecord): Promise<void> {
  try {
    const updated = await updateShare(record.code, { allowDownload: !record.allowDownload });
    shares.value = shares.value.map((item) => (item.code === updated.code ? updated : item));
    updateFeedback(updated.allowDownload ? "已开启原文件下载权限" : "已设为仅限在线播放", "success");
  } catch (error) {
    toastError(error, "修改分享权限失败");
  }
}

export async function revokeShareByCode(code: string): Promise<void> {
  try {
    await deleteShare(code);
    shares.value = shares.value.filter((item) => item.code !== code);
    updateFeedback("已撤销分享链接", "success");
  } catch (error) {
    toastError(error, "撤销分享失败");
  }
}

export async function removeExpiredShares(): Promise<void> {
  try {
    const res = await clearExpiredShares();
    await refreshShares(true);
    updateFeedback(`已清理 ${res.removedCount} 条过期分享`, "success");
  } catch (error) {
    toastError(error, "清理过期分享失败");
  }
}
