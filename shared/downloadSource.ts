import type { SourceType } from "./models.js";

export function inferSourceType(url: string): SourceType {
  const normalized = url.trim().toLowerCase();
  if (normalized.startsWith("magnet:")) {
    return "magnet";
  }

  try {
    const parsed = new URL(url);
    const pathname = decodeURIComponent(parsed.pathname).toLowerCase();
    if (pathname.endsWith(".torrent")) {
      return "torrent-url";
    }
  } catch {
    // Let the request schema return the validation error for invalid URLs.
  }

  return "direct";
}

export function sourceTypeLabel(sourceType: SourceType): string {
  switch (sourceType) {
    case "magnet":
      return "Magnet";
    case "torrent-url":
      return "Torrent URL";
    case "direct":
    default:
      return "HTTP(S)";
  }
}
