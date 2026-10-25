import type { DownloadJob } from "@shared/index";

export function formatBytes(value: number): string {
  if (value <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const size = value / 1024 ** exponent;
  return `${size.toFixed(size >= 10 || exponent === 0 ? 0 : 1)} ${units[exponent]}`;
}

export function formatSpeed(value: number): string {
  return `${formatBytes(value)}/s`;
}

export function progressPercent(download: DownloadJob): number {
  if (!download.totalLength) {
    return 0;
  }
  return Math.min(100, Math.round((download.completedLength / download.totalLength) * 100));
}

export function formatDateTime(value: string | null, fallback = "未刷新"): string {
  if (!value) {
    return fallback;
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value.slice(0, 16);
  }
  const pad = (input: number) => String(input).padStart(2, "0");
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function airDayLabel(value: number | null): string {
  const labels = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
  return typeof value === "number" && labels[value] ? labels[value] : "未定";
}

/**
 * 首播时刻（ISO 瞬间）→ 本地时区的星期。
 *
 * 番剧的「周几」按用户本地时间算：深夜番在日本和本地就是不同的天
 * （JST 周五 00:30 = 本地周四 23:30），两边都叫得通，这里统一用本地。
 * 拿不到时刻时调用方回退到服务端算好的 airDay。
 */
export function broadcastWeekday(broadcastAt: string | null | undefined): number | null {
  if (!broadcastAt) {
    return null;
  }
  const date = new Date(broadcastAt);
  return Number.isNaN(date.getTime()) ? null : date.getDay();
}

/** 首播时刻 → 「每周四 23:30」。拿不到就返回 null，由调用方决定退回什么。 */
export function broadcastLabel(broadcastAt: string | null | undefined): string | null {
  const weekday = broadcastWeekday(broadcastAt);
  if (weekday === null) {
    return null;
  }
  const date = new Date(broadcastAt as string);
  const pad = (input: number) => String(input).padStart(2, "0");
  return `每${airDayLabel(weekday)} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function buildPaginationItems(currentPage: number, totalPages: number): Array<number | "ellipsis"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, index) => index + 1);
  }

  const items: Array<number | "ellipsis"> = [1];
  const windowStart = Math.max(2, currentPage - 1);
  const windowEnd = Math.min(totalPages - 1, currentPage + 1);

  if (windowStart > 2) {
    items.push("ellipsis");
  }
  for (let page = windowStart; page <= windowEnd; page += 1) {
    items.push(page);
  }
  if (windowEnd < totalPages - 1) {
    items.push("ellipsis");
  }
  items.push(totalPages);
  return items;
}

export function clampPage(page: number, totalPages: number): number {
  return Math.min(Math.max(1, page), Math.max(1, totalPages));
}
