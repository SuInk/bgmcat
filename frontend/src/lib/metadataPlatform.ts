/** 信息平台在界面上的显示名。后端给的是 id，中文界面上要有个短名。 */
const PLATFORM_LABELS: Record<string, string> = {
  // 本地 bangumi-data 索引。它带着放送时刻和蜜柑编号，是最有价值的一份
  "bangumi-data": "本地",
  bangumi: "Bangumi",
  anilist: "AniList",
  mal: "MAL",
  tmdb: "TMDB"
};

export function metadataPlatformLabel(id: string): string {
  return PLATFORM_LABELS[id] ?? id;
}

/**
 * 一个平台的评分怎么写。
 * **各平台各自的满分照写出来**（AniList 是 100 分制），绝不折算成统一刻度——
 * 折算会让人误以为几个平台的分能直接比大小。
 */
export function formatPlatformScore(score: number | null | undefined, scoreMax: number | undefined): string | null {
  if (typeof score !== "number" || !Number.isFinite(score) || score <= 0) {
    return null;
  }
  const max = scoreMax ?? 10;
  return max === 100 ? `${Math.round(score)} / 100` : `${score.toFixed(1)} / ${max}`;
}
