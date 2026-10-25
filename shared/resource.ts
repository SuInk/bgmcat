/**
 * 跨源资源标识（resource key）。
 *
 * 同一个种子会同时出现在蜜柑、動漫花園、Nyaa 上，标题、页面地址、guid 全都不一样，
 * 但它就是同一份资源：展示要合并成一行，下载只能入队一次。
 *
 * 优先用 infoHash —— 它是种子内容的哈希，天然跨站唯一。
 * 拿不到 infoHash（只给 .torrent 链接、还没下下来）时退化成指纹：
 * 归一化标题 + 集数 + 体积档位。指纹只是尽力而为，宁可少合并也不要错合并。
 */

/** 从 magnet 链接里抠出 infoHash，归一成 40 位小写 hex；抠不出返回 null。 */
export function extractInfoHashFromMagnet(magnetUrl: string | null | undefined): string | null {
  if (!magnetUrl) {
    return null;
  }
  const match = magnetUrl.match(/xt=urn:btih:([a-zA-Z0-9]+)/i) ?? magnetUrl.match(/btih:([a-zA-Z0-9]+)/i);
  return normalizeInfoHash(match?.[1] ?? null);
}

/** 从任意 URL（.torrent 路径、蜜柑的 /Home/Episode/<hash>）里抠出 40 位 infoHash。 */
export function extractInfoHashFromUrl(url: string | null | undefined): string | null {
  if (!url) {
    return null;
  }
  return normalizeInfoHash(url.match(/\b([a-fA-F0-9]{40})\b/)?.[1] ?? null);
}

/**
 * 只接受 40 位 hex（v1 infoHash）。
 * base32（32 字符）和 v2 的 64 位 hex 不做转换：与 40 位 hex 混在一起比对
 * 只会产生「看起来不等其实相等」的假阴性，还不如统一走指纹。
 */
export function normalizeInfoHash(value: string | null | undefined): string | null {
  const trimmed = value?.trim().toLowerCase() ?? "";
  return /^[a-f0-9]{40}$/.test(trimmed) ? trimmed : null;
}

/** 把 "6.6 GiB" / "48.5GB" / "1,024 MB" 这类文本换算成字节；认不出返回 null。 */
export function parseSizeTextToBytes(sizeText: string | null | undefined): number | null {
  if (!sizeText) {
    return null;
  }
  const match = sizeText.replace(/,/g, "").match(/([\d.]+)\s*([KMGT]?)i?B\b/i);
  if (!match) {
    return null;
  }
  const amount = Number.parseFloat(match[1] ?? "");
  if (!Number.isFinite(amount) || amount <= 0) {
    return null;
  }
  const exponent = { "": 0, K: 1, M: 2, G: 3, T: 4 }[(match[2] ?? "").toUpperCase()] ?? 0;
  return Math.round(amount * 1024 ** exponent);
}

/**
 * 标题归一化：剥掉发布者、站点后缀、标点和空白，只留下字母数字与中日文字。
 * 目的是让「[字幕组] 番名 - 01 [1080p]」和「[Group] 番名 01 (1080p)」落到同一串上。
 */
export function normalizeResourceTitle(title: string): string {
  return title
    .toLowerCase()
    .replace(/\.(torrent|mkv|mp4)\b/g, " ")
    .replace(/[^a-z0-9一-龥぀-ヿ]+/g, "");
}

/**
 * 体积档位。两个源报同一个种子的体积可能差几个字节（有的算种子文件、有的算内容），
 * 精确到字节比对等于永远不相等，所以按 1% 粒度分桶。
 */
function sizeBucket(sizeBytes: number | null): string {
  if (!sizeBytes || sizeBytes <= 0) {
    return "?";
  }
  return String(Math.round(Math.log(sizeBytes) / Math.log(1.01)));
}

export interface ResourceKeyInput {
  infoHash?: string | null;
  title: string;
  episodeNumber?: number | null;
  resolution?: string | null;
  sizeBytes?: number | null;
}

/**
 * 算出跨源标识。
 * 有 infoHash 就是 `hash:<40hex>`，没有就是 `fp:<归一化标题>|<集数>|<分辨率>|<体积档>`。
 *
 * 注意两条指纹相等只说明「很可能是同一份资源」。指纹里带上分辨率与体积，
 * 是为了避免把同一集的不同压制版本错并成一条 —— 那会让用户以为某个画质不存在。
 */
export function computeResourceKey(input: ResourceKeyInput): string {
  const infoHash = normalizeInfoHash(input.infoHash);
  if (infoHash) {
    return `hash:${infoHash}`;
  }
  const parts = [
    normalizeResourceTitle(input.title),
    input.episodeNumber === null || input.episodeNumber === undefined ? "?" : String(input.episodeNumber),
    input.resolution ?? "?",
    sizeBucket(input.sizeBytes ?? null)
  ];
  return `fp:${parts.join("|")}`;
}
