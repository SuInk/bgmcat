import type { DownloadJob, FeedItem } from "@shared/index";

function prettifyDownloadFallback(value: string): string {
  try {
    const url = new URL(value);
    const filename = decodeURIComponent(url.pathname.split("/").filter(Boolean).pop() ?? "");
    return filename || value;
  } catch {
    return value;
  }
}

/**
 * 去掉剥离集数/画质标记后残留的连接符尾巴，例如「尼古喵喵 - 」「某某番 ·」。
 * 中文标题里的破折号、间隔号、下划线都可能被留在头尾，看起来像没清干净的脏数据。
 */
export function trimTitleSeparators(value: string): string {
  return value
    .replace(/\s+/g, " ")
    .replace(/^[\s\-–—_~·・/|:：、,，.。]+/u, "")
    .replace(/[\s\-–—_~·・/|:：、,，]+$/u, "")
    .trim();
}

export function isPlaceholderDownloadTitle(value: string): boolean {
  const title = value.trim();
  if (!title) {
    return true;
  }
  return (
    /^[0-9a-f]{16,}\.torrent$/i.test(title) ||
    /^[0-9a-f]{16,}$/i.test(title) ||
    /^[0-9a-f-]{20,}$/i.test(title) ||
    /^unknown$/i.test(title)
  );
}

function decodeTitleEntities(value: string): string {
  if (!value.includes("&")) {
    return value;
  }
  return value
    .replace(/&amp;/gi, "&")
    .replace(/&quot;/gi, '"')
    .replace(/&#39;|&apos;/gi, "'")
    .replace(/&lt;/gi, "<")
    .replace(/&gt;/gi, ">");
}

export function downloadDisplayTitle(download: DownloadJob, feedItemById: Map<number, FeedItem>): string {
  const directTitle = decodeTitleEntities(download.feedItemTitle?.trim() ?? "");
  if (directTitle && !isPlaceholderDownloadTitle(directTitle)) {
    if (directTitle === download.feedItemSourceUrl || directTitle === download.feedItemLink) {
      return prettifyDownloadFallback(directTitle);
    }
    return directTitle;
  }

  const feedItem = feedItemById.get(download.feedItemId);
  if (feedItem && !isPlaceholderDownloadTitle(feedItem.title)) {
    if (feedItem.title === feedItem.sourceUrl || feedItem.title === feedItem.link) {
      return prettifyDownloadFallback(feedItem.title);
    }
    return decodeTitleEntities(feedItem.title);
  }

  const fallback =
    download.feedItemSourceUrl ?? download.feedItemLink ?? feedItem?.sourceUrl ?? feedItem?.link ?? directTitle;
  const prettyFallback = prettifyDownloadFallback(fallback);
  return isPlaceholderDownloadTitle(prettyFallback) ? download.gid : prettyFallback;
}

export function downloadDetailSearchKeyword(download: DownloadJob, feedItemById: Map<number, FeedItem>): string {
  const title = downloadDisplayTitle(download, feedItemById);
  const withoutExt = title.replace(/\.(?:mkv|mp4|avi|mov|webm|flv|ass|srt|torrent)$/i, " ");
  let debracketed = withoutExt
    .replace(/\[[^\]]+\]/g, " ")
    .replace(/【[^】]+】/g, " ")
    .replace(/\([^)]+\)/g, " ");
  if (!/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}A-Za-z]/u.test(debracketed)) {
    const secondBracket = withoutExt.match(/^\s*(?:\[[^\]]+\]|【[^】]+】)\s*(?:\[([^\]]+)\]|【([^】]+)】)/);
    if (secondBracket) {
      debracketed = secondBracket[1] ?? secondBracket[2] ?? debracketed;
    }
  }
  const cleaned = debracketed
    .replace(/\s+-\s*(?:第\s*)?\d{1,3}(?:話|话|集)?(?:v\d+)?(?:\s|$).*$/i, " ")
    .replace(/(?:第\s*)\d{1,3}\s*(?:話|话|集).*$/i, " ")
    .replace(/\bS\d{1,2}(?:\s*E\d{1,3})?\b.*$/i, " ")
    .replace(/\bSeason\s*\d+\b.*$/i, " ")
    .replace(/第\s*\d+\s*季.*$/i, " ")
    .replace(/\b(?:EP?|Episode)\s*\d{1,3}\b.*$/i, " ")
    .replace(/\b(?:1080p|720p|2160p|4k|hevc|avc|x26[45]|aac|flac|web-?rip|web-?dl|baha|gb|big5|chs|cht)\b/gi, " ")
    .replace(/(?:日英双语|多国字幕|简繁内封|简日内嵌|繁日内嵌|内嵌|外挂|字幕组|简日|繁日|简体|繁体|合集|先行|正式)/g, " ")
    .replace(/\s+/g, " ")
    .trim();

  return trimTitleSeparators(cleaned) || title;
}
