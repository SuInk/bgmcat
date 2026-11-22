import type {
  AnimeDetail,
  AppSettings,
  DownloadJob,
  FavoriteAnime,
  FeedItem,
  HiddenAnime,
  HomepageAnime,
  LogEntry,
  Subscription,
  SystemStatus
} from "@shared/index";

const nowIso = () => new Date().toISOString();

const FALLBACK_CATALOG_ANIME: HomepageAnime[] = [
  {
    animeId: "400602",
    bangumiId: "400602",
    title: "葬送的芙莉莲",
    imageUrl: "https://lain.bgm.tv/r/400/pic/cover/l/13/c5/400602_ZI8Y9.jpg",
    searchKeyword: "葬送的芙莉莲",
    airDay: 5,
    isOnAir: false,
    airStatus: "finished",
    airDate: "2023-09-29",
    format: "TV",
    totalEpisodes: 28,
    rank: 2,
    tags: ["奇幻", "冒险", "治愈", "日本"],
    isRestricted: false,
    rating: 8.6,
    source: "bangumi",
    mikanId: null,
    officialSite: "https://frieren-anime.jp/"
  },
  {
    animeId: "379709",
    bangumiId: "379709",
    title: "孤独摇滚！",
    imageUrl: "https://lain.bgm.tv/r/400/pic/cover/l/e2/e7/379709_w32A3.jpg",
    searchKeyword: "孤独摇滚",
    airDay: 6,
    isOnAir: false,
    airStatus: "finished",
    airDate: "2022-10-08",
    format: "TV",
    totalEpisodes: 12,
    rank: 19,
    tags: ["音乐", "日常", "搞笑", "校园", "日本"],
    isRestricted: false,
    rating: 8.4,
    source: "bangumi",
    mikanId: null,
    officialSite: "https://bocchi.rocks/"
  },
  {
    animeId: "10380",
    bangumiId: "10380",
    title: "命运石之门",
    imageUrl: "https://lain.bgm.tv/r/400/pic/cover/l/a9/79/10380_YwP4R.jpg",
    searchKeyword: "命运石之门",
    airDay: 3,
    isOnAir: false,
    airStatus: "finished",
    airDate: "2011-04-05",
    format: "TV",
    totalEpisodes: 24,
    rank: 4,
    tags: ["科幻", "悬疑", "推理", "智斗", "日本"],
    isRestricted: false,
    rating: 8.8,
    source: "bangumi",
    mikanId: null,
    officialSite: null
  },
  {
    animeId: "443462",
    bangumiId: "443462",
    title: "败犬女主太多了！",
    imageUrl: "https://lain.bgm.tv/r/400/pic/cover/l/38/6f/443462_R0zZ3.jpg",
    searchKeyword: "败犬女主太多了",
    airDay: 6,
    isOnAir: false,
    airStatus: "finished",
    airDate: "2024-07-13",
    format: "TV",
    totalEpisodes: 12,
    rank: 95,
    tags: ["恋爱", "校园", "青春", "搞笑", "日本"],
    isRestricted: false,
    rating: 8.0,
    source: "bangumi",
    mikanId: null,
    officialSite: null
  },
  {
    animeId: "425909",
    bangumiId: "425909",
    title: "迷宫饭",
    imageUrl: "https://lain.bgm.tv/r/400/pic/cover/l/00/57/425909_6741i.jpg",
    searchKeyword: "迷宫饭",
    airDay: 4,
    isOnAir: false,
    airStatus: "finished",
    airDate: "2024-01-04",
    format: "TV",
    totalEpisodes: 24,
    rank: 88,
    tags: ["奇幻", "冒险", "美食", "搞笑", "日本"],
    isRestricted: false,
    rating: 8.0,
    source: "bangumi",
    mikanId: null,
    officialSite: null
  },
  {
    animeId: "9717",
    bangumiId: "9717",
    title: "魔法少女小圆",
    imageUrl: "https://lain.bgm.tv/r/400/pic/cover/l/cb/57/9717_sAVag.jpg",
    searchKeyword: "魔法少女小圆",
    airDay: 4,
    isOnAir: false,
    airStatus: "finished",
    airDate: "2011-01-06",
    format: "TV",
    totalEpisodes: 12,
    rank: 11,
    tags: ["奇幻", "悬疑", "原创", "治愈", "日本"],
    isRestricted: false,
    rating: 8.6,
    source: "bangumi",
    mikanId: null,
    officialSite: null
  }
];

function mapBangumiSubject(raw: Record<string, unknown>, fallbackAirDay: number | null = null): HomepageAnime | null {
  const id = String(raw.id ?? "").trim();
  if (!id) return null;
  const nameCn = typeof raw.name_cn === "string" ? raw.name_cn.trim() : "";
  const name = typeof raw.name === "string" ? raw.name.trim() : "";
  const title = nameCn || name;
  if (!title) return null;

  const images = (raw.images ?? {}) as Record<string, unknown>;
  let imageUrl = String(images.common ?? images.large ?? images.medium ?? "").trim() || null;
  if (imageUrl && imageUrl.startsWith("http://")) {
    imageUrl = "https://" + imageUrl.slice("http://".length);
  }

  const ratingObj = (raw.rating ?? {}) as Record<string, unknown>;
  const score = typeof ratingObj.score === "number" && ratingObj.score > 0 ? Number(ratingObj.score.toFixed(1)) : null;
  const rank =
    typeof ratingObj.rank === "number" && ratingObj.rank > 0
      ? ratingObj.rank
      : typeof raw.rank === "number" && raw.rank > 0
        ? raw.rank
        : null;

  const airDate = typeof raw.date === "string" && raw.date ? raw.date : typeof raw.air_date === "string" ? raw.air_date : null;
  const platform = typeof raw.platform === "string" ? raw.platform.trim() : "";
  const format = platform === "剧场版" || platform === "OVA" || platform === "WEB" || platform === "TV" ? platform : "TV";
  const eps = typeof raw.eps === "number" && raw.eps > 0 ? raw.eps : typeof raw.total_episodes === "number" && raw.total_episodes > 0 ? raw.total_episodes : null;

  const tags: string[] = [];
  if (Array.isArray(raw.tags)) {
    for (const item of raw.tags.slice(0, 8)) {
      if (item && typeof item === "object" && typeof (item as Record<string, unknown>).name === "string") {
        tags.push(String((item as Record<string, unknown>).name));
      }
    }
  }

  let airDay = fallbackAirDay;
  if (airDay === null && airDate) {
    const d = new Date(airDate + "T00:00:00");
    if (!Number.isNaN(d.getTime())) airDay = d.getDay();
  }

  let airStatus: "airing" | "finished" | "upcoming" = "finished";
  if (airDate) {
    const start = new Date(airDate + "T00:00:00").getTime();
    const now = Date.now();
    if (start > now) {
      airStatus = "upcoming";
    } else if (now - start < 110 * 86400_000) {
      airStatus = "airing";
    }
  }

  return {
    animeId: id,
    bangumiId: id,
    title,
    imageUrl,
    searchKeyword: title,
    airDay,
    isOnAir: airStatus === "airing",
    airStatus,
    airDate,
    format,
    totalEpisodes: eps,
    rank,
    tags,
    isRestricted: Boolean(raw.nsfw),
    rating: score,
    source: "bangumi",
    mikanId: null,
    officialSite: null
  };
}

export function installDemoMode(): void {
  const realFetch = window.fetch.bind(window);

  let demoSettings: AppSettings = {
    dianaMcpConfigPath: "~/.diana/data/.mcp.json",
    bindHost: "127.0.0.1",
    port: 39123,
    lanAccessEnabled: true,
    token: "demo-static-mcp-token-bgmcat-preview-0001",
    corsOrigins: [],
    pollIntervalMinutes: 30,
    downloadDir: "/Users/demo/Movies/BangumiCat",
    cloudProvider: "google",
    cloudArchiveEnabled: true,
    googleClientId: "demo-client.apps.googleusercontent.com",
    googleClientSecret: "••••••••••••",
    cloudDriveFolderId: "1BgmCatArchiveFolderDemo",
    googleRedirectUri: "http://127.0.0.1:39123/api/cloud/callback",
    onedriveClientId: "",
    onedriveClientSecret: "",
    onedriveRedirectUri: "",
    onedriveFolderId: "root",
    webdavUrl: "",
    webdavUsername: "",
    webdavPassword: "",
    webdavRootPath: "Bangumi Cat",
    cloudDeleteLocal: true,
    downloadClient: "native",
    aria2RpcUrl: "http://127.0.0.1:6800/jsonrpc",
    aria2RpcHost: "127.0.0.1",
    aria2RpcPort: 16800,
    aria2RpcSecret: "",
    aria2SessionPath: "",
    aria2Enabled: true,
    aria2SeedEnabled: false,
    btTrackers: [],
    defaultSaveTo: "cloud",
    qbittorrentBaseUrl: "http://127.0.0.1:8080",
    qbittorrentUsername: "admin",
    qbittorrentPassword: "",
    qbittorrentEnabled: false,
    transmissionRpcUrl: "http://127.0.0.1:9091/transmission/rpc",
    transmissionUsername: "",
    transmissionPassword: "",
    mikanBaseUrl: "https://mikanani.me",
    providerMirrors: {},
    mikanDirectAccessMode: "prefer",
    mikanDirectAccessEnabled: true,
    mikanResolverIps: [],
    localAiEnabled: false,
    localAiBaseUrl: "http://127.0.0.1:11434",
    localAiApiKey: "",
    localAiModel: "gemma3:4b",
    localAiSystemPrompt: "",
    homepageSortMode: "default",
    multiSidebarEnabled: true,
    tunnelEnabled: true,
    tunnelMode: "quick",
    tunnelBinaryPath: "",
    tunnelAutoRestart: true,
    tunnelHostname: "",
    tunnelCredentialMode: "credentials-file",
    tunnelCredentialsFile: "",
    tunnelId: "",
    tunnelToken: "",
    exposeMcpPublicly: false,
    githubOAuthEnabled: false,
    githubClientId: "",
    githubClientSecret: "",
    githubAllowedLogins: [],
    googleOAuthEnabled: false,
    googleAuthClientId: "",
    googleAuthClientSecret: "",
    googleAllowedEmails: [],
    dianaAuthEnabled: false,
    dianaAuthBaseUrl: "",
    oidcEnabled: false,
    oidcDisplayName: "SSO",
    oidcIssuerUrl: "",
    oidcClientId: "",
    oidcClientSecret: "",
    oidcAllowedUsers: [],
    ffmpegPath: "",
    updateAutoDownload: true,
    updateAutoInstall: false,
    updateMirror: "auto",
    updateChannel: "stable",
    launchAtLogin: false,
    minimizeToTray: true,
    enabledResourceProviders: ["mikan", "animegarden", "dmhy", "bangumimoe", "nyaa"],
    resourceProviderOrder: ["mikan", "animegarden", "dmhy", "bangumimoe", "acgnx", "acgrip", "nyaa"],
    enabledMetadataPlatforms: ["bangumi", "anilist"],
    tmdbApiKey: "",
    themeAccent: "#0041fd"
  };

  let hiddenAnime: HiddenAnime[] = [];
  let favorites: FavoriteAnime[] = [
    {
      ...FALLBACK_CATALOG_ANIME[0],
      mikanKeyword: "葬送的芙莉莲",
      rssUrl: "https://mikanani.me/RSS/Search?searchstr=%E8%91%AC%E9%80%81%E7%9A%84%E8%8A%99%E8%8E%89%E8%8E%B2",
      subscriptionId: 1,
      subscriptionName: "葬送的芙莉莲 · 自动归档网盘",
      createdAt: nowIso(),
      updatedAt: nowIso(),
      subscriptionAutoDownload: true,
      subscriptions: [
        {
          id: 1,
          providerId: "mikan",
          providerName: "蜜柑计划",
          name: "葬送的芙莉莲 · 自动归档网盘",
          url: "https://mikanani.me/RSS/Search?searchstr=%E8%91%AC%E9%80%81%E7%9A%84%E8%8A%99%E8%8E%89%E8%8E%B2",
          enabled: true,
          autoDownload: true
        }
      ]
    }
  ];

  let subscriptions: Subscription[] = [
    {
      id: 1,
      name: "葬送的芙莉莲 · 自动归档网盘",
      url: "https://mikanani.me/RSS/Search?searchstr=%E8%91%AC%E9%80%81%E7%9A%84%E8%8A%99%E8%8E%89%E8%8E%B2",
      enabled: true,
      downloadDirOverride: null,
      rule: {
        includeKeywords: ["1080p", "简日"],
        excludeKeywords: ["720p"],
        preferredResolutions: ["1080p"],
        autoDownload: true,
        skipBatch: true,
        preferredSubgroups: ["LoliHouse", "樱都字幕组"]
      },
      lastFetchedAt: nowIso(),
      lastError: null,
      createdAt: nowIso(),
      updatedAt: nowIso(),
      downloadedEpisodes: [26, 27, 28],
      animeId: "400602",
      providerId: "mikan"
    },
    {
      id: 2,
      name: "孤独摇滚！ · 1080p 收藏",
      url: "https://mikanani.me/RSS/Search?searchstr=%E5%AD%A4%E7%8B%AC%E6%91%87%E6%BB%9A",
      enabled: true,
      downloadDirOverride: null,
      rule: {
        includeKeywords: ["1080p"],
        excludeKeywords: [],
        preferredResolutions: ["1080p"],
        autoDownload: true,
        skipBatch: true,
        preferredSubgroups: ["北宇治字幕组"]
      },
      lastFetchedAt: nowIso(),
      lastError: null,
      createdAt: nowIso(),
      updatedAt: nowIso(),
      downloadedEpisodes: [12],
      animeId: "379709",
      providerId: "mikan"
    }
  ];

  const feedItems: FeedItem[] = [
    {
      id: 101,
      subscriptionId: 1,
      title: "[LoliHouse] 葬送的芙莉莲 / Sousou no Frieren - 28 [WebRip 1080p HEVC-10bit AAC 简繁内封]",
      imageUrl: FALLBACK_CATALOG_ANIME[0].imageUrl,
      guid: "demo-guid-101",
      link: "https://mikanani.me",
      publishedAt: nowIso(),
      sourceType: "magnet",
      sourceUrl: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
      infoHash: "0123456789abcdef0123456789abcdef01234567",
      normalizedTitle: "葬送的芙莉莲 28",
      dedupeKey: "demo-101",
      matched: true,
      matchReason: "命中订阅规则 (LoliHouse · 1080p)",
      downloadStatus: "complete",
      createdAt: nowIso(),
      episodeNumber: 28,
      isBatch: false,
      subgroup: "LoliHouse",
      resolution: "1080p",
      resourceKey: "0123456789abcdef0123456789abcdef01234567"
    }
  ];

  let downloads: DownloadJob[] = [
    {
      id: 1,
      feedItemId: 101,
      gid: "native-bt-demo-001",
      sourceUrl: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
      feedItemTitle: "[LoliHouse] 葬送的芙莉莲 / Sousou no Frieren - 28 [WebRip 1080p HEVC-10bit AAC 简繁内封].mkv",
      feedItemImageUrl: FALLBACK_CATALOG_ANIME[0].imageUrl,
      feedItemSubscriptionId: 1,
      status: "complete",
      totalLength: 684_200_000,
      completedLength: 684_200_000,
      downloadSpeed: 0,
      uploadSpeed: 0,
      errorMessage: null,
      saveTo: "cloud",
      cloudArchive: {
        status: "archived",
        folderId: "1BgmCatArchiveFolderDemo",
        files: [
          {
            localPath: "",
            remoteName: "[LoliHouse] 葬送的芙莉莲 - 28 [1080p].mkv",
            remoteId: "drive-file-demo-28",
            size: 684_200_000,
            md5: "d41d8cd98f00b204e9800998ecf8427e"
          }
        ]
      },
      files: [{ path: "[LoliHouse] 葬送的芙莉莲 - 28 [1080p].mkv", size: 684_200_000, uploadedBytes: 684_200_000, verified: true }],
      createdAt: nowIso(),
      updatedAt: nowIso()
    },
    {
      id: 2,
      feedItemId: 102,
      gid: "native-bt-demo-002",
      sourceUrl: "magnet:?xt=urn:btih:fedcba9876543210fedcba9876543210fedcba98",
      feedItemTitle: "[北宇治字幕组] 孤独摇滚！ / Bocchi the Rock! - 12 [WebRip 1080p HEVC 简日内嵌].mp4",
      feedItemImageUrl: FALLBACK_CATALOG_ANIME[1].imageUrl,
      feedItemSubscriptionId: 2,
      status: "active",
      totalLength: 542_000_000,
      completedLength: 368_560_000,
      downloadSpeed: 14_820_000,
      uploadSpeed: 620_000,
      connectionCount: 38,
      errorMessage: null,
      filePath: "/Users/demo/Movies/BangumiCat/Bocchi-12.mp4",
      saveTo: "cloud",
      files: [{ path: "/Users/demo/Movies/BangumiCat/Bocchi-12.mp4", size: 542_000_000, uploadedBytes: 0, verified: false }],
      createdAt: nowIso(),
      updatedAt: nowIso()
    }
  ];

  const logs: LogEntry[] = [
    { id: 1, level: "info", message: "[NativeEngine] 纯 Go 内置下载引擎已就绪（支持 HTTP 分段与 BitTorrent 边下边播）", createdAt: nowIso() },
    { id: 2, level: "info", message: "[GoogleDrive] 葬送的芙莉莲 第 28 话 MD5 校验通过，已归档至云盘并释放本地空间", createdAt: nowIso() },
    { id: 3, level: "info", message: "[LAN] 局域网手机直连已启用：http://192.168.1.20:39123", createdAt: nowIso() },
    { id: 4, level: "info", message: "[Tunnel] Cloudflare 快速穿透在线：https://demo-cat.trycloudflare.com", createdAt: nowIso() }
  ];

  function buildStatus(): SystemStatus {
    return {
      appVersion: "0.2.2",
      activeHost: "127.0.0.1",
      activePort: 39123,
      configuredHost: "127.0.0.1",
      configuredPort: 39123,
      lanAccessEnabled: demoSettings.lanAccessEnabled,
      lanUrls: demoSettings.lanAccessEnabled ? ["http://192.168.1.20:39123"] : [],
      apiBaseUrl: "http://127.0.0.1:39123",
      mcpSseUrl: "http://127.0.0.1:39123/mcp/sse",
      tokenHeaderName: "Authorization",
      tokenConfigured: true,
      localAuthBypassEnabled: false,
      aria2Online: true,
      qbittorrentOnline: false,
      downloadClient: demoSettings.downloadClient,
      rssPollingEnabled: true,
      restartRequired: false,
      tunnelEnabled: demoSettings.tunnelEnabled,
      tunnelState: demoSettings.tunnelEnabled ? "online" : "stopped",
      tunnelPublicUrl: demoSettings.tunnelEnabled ? "https://demo-cat.trycloudflare.com" : null,
      publicExposureActive: demoSettings.tunnelEnabled
    };
  }

  function jsonResponse(data: unknown, status = 200): Response {
    return new Response(JSON.stringify({ ok: status < 400, data }), {
      status,
      headers: { "Content-Type": "application/json; charset=utf-8" }
    });
  }

  async function fetchDemoHome(): Promise<HomepageAnime[]> {
    try {
      const res = await realFetch("https://api.bgm.tv/calendar", { signal: AbortSignal.timeout(6500) });
      if (res.ok) {
        const days = (await res.json()) as Array<{ weekday?: { id?: number }; items?: Array<Record<string, unknown>> }>;
        const out: HomepageAnime[] = [];
        for (const group of days) {
          const wd = typeof group.weekday?.id === "number" ? group.weekday.id % 7 : 0;
          for (const raw of group.items ?? []) {
            const item = mapBangumiSubject(raw, wd);
            if (item) {
              item.isOnAir = true;
              item.airStatus = "airing";
              out.push(item);
            }
          }
        }
        if (out.length > 0) return out;
      }
    } catch {
      // Fall back to built-in fixtures when offline or blocked.
    }
    return FALLBACK_CATALOG_ANIME.map((x, idx) => ({ ...x, isOnAir: true, airStatus: "airing", airDay: (idx % 7) }));
  }

  async function fetchDemoCatalog(url: URL): Promise<{ items: HomepageAnime[]; total: number; offset: number; limit: number; hasMore: boolean }> {
    const keyword = (url.searchParams.get("keyword") ?? "").trim();
    const format = url.searchParams.get("format") ?? "all";
    const region = url.searchParams.get("region") ?? "all";
    const status = url.searchParams.get("status") ?? "all";
    const year = url.searchParams.get("year") ?? "all";
    const season = url.searchParams.get("season") ?? "all";
    const genre = url.searchParams.get("genre") ?? "all";
    const minRating = Number(url.searchParams.get("minRating") ?? "0") || 0;
    const sort = url.searchParams.get("sort") ?? "heat";
    const limit = Math.min(48, Math.max(1, Number(url.searchParams.get("limit") ?? "28") || 28));
    const offset = Math.max(0, Number(url.searchParams.get("offset") ?? "0") || 0);

    try {
      const tags: string[] = [];
      if (format !== "all") tags.push(format);
      if (region === "jp") tags.push("日本");
      else if (region === "cn") tags.push("中国");
      else if (region === "west") tags.push("欧美");
      if (genre !== "all") tags.push(genre);

      const airDate: string[] = [];
      const nowStr = new Date().toISOString().slice(0, 10);
      if (/^\d{4}$/.test(year)) {
        const seasonRanges: Record<string, [string, string]> = {
          winter: [`${year}-01-01`, `${year}-03-31`],
          spring: [`${year}-04-01`, `${year}-06-30`],
          summer: [`${year}-07-01`, `${year}-09-30`],
          autumn: [`${year}-10-01`, `${year}-12-31`]
        };
        const [start, end] = seasonRanges[season] ?? [`${year}-01-01`, `${year}-12-31`];
        airDate.push(`>=${start}`, `<=${end}`);
      } else if (year.includes("-")) {
        const [from, to] = year.split("-");
        airDate.push(`>=${from}-01-01`, `<=${to}-12-31`);
      } else if (year === "older") {
        airDate.push("<=1989-12-31");
      } else if (status === "upcoming") {
        airDate.push(`>${nowStr}`);
      } else if (status === "airing") {
        const recent = new Date(Date.now() - 105 * 86400_000).toISOString().slice(0, 10);
        airDate.push(`>=${recent}`, `<=${nowStr}`);
      } else if (status === "finished") {
        airDate.push(`<=${nowStr}`);
      }

      const filter: Record<string, unknown> = { type: [2], nsfw: false };
      if (tags.length) filter.tag = tags;
      if (airDate.length) filter.air_date = airDate;
      if (minRating > 0) filter.rating = [`>=${minRating.toFixed(1)}`];
      if (!keyword && (sort === "rank" || sort === "score")) filter.rank = [">0"];

      const bgmSort = sort === "score" ? "score" : sort === "rank" ? "rank" : "heat";
      const res = await realFetch(`https://api.bgm.tv/v0/search/subjects?limit=${limit}&offset=${offset}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ keyword, sort: bgmSort, filter }),
        signal: AbortSignal.timeout(7000)
      });
      if (res.ok) {
        const payload = (await res.json()) as { data?: Array<Record<string, unknown>>; total?: number };
        const items = (payload.data ?? []).map((x) => mapBangumiSubject(x)).filter((x): x is HomepageAnime => Boolean(x));
        if (sort === "date") {
          items.sort((a, b) => (b.airDate ?? "").localeCompare(a.airDate ?? ""));
        }
        const total = typeof payload.total === "number" ? payload.total : items.length;
        return { items, total, offset, limit, hasMore: offset + items.length < total && items.length > 0 };
      }
    } catch {
      // Fallback below
    }

    let items = FALLBACK_CATALOG_ANIME.filter((item) => {
      if (keyword && !item.title.includes(keyword)) return false;
      if (format !== "all" && item.format !== format) return false;
      if (genre !== "all" && !(item.tags ?? []).includes(genre)) return false;
      if (minRating > 0 && (item.rating ?? 0) < minRating) return false;
      if (/^\d{4}$/.test(year) && !(item.airDate ?? "").startsWith(year)) return false;
      return true;
    });
    if (sort === "score") items = [...items].sort((a, b) => (b.rating ?? 0) - (a.rating ?? 0));
    if (sort === "rank") items = [...items].sort((a, b) => (a.rank ?? 9999) - (b.rank ?? 9999));
    return { items, total: items.length, offset: 0, limit, hasMore: false };
  }

  async function fetchDemoDetail(animeId: string): Promise<AnimeDetail> {
    let base = FALLBACK_CATALOG_ANIME.find((x) => x.animeId === animeId) ?? FALLBACK_CATALOG_ANIME[0];
    let summary = "演示模式下的番剧简介：安装桌面端后可自动拉取多源磁力/种子资源，使用内置自研引擎边下边播，并在校验完成后自动归档至 Google Drive。";
    const tags = [...(base.tags ?? [])];
    try {
      const res = await realFetch(`https://api.bgm.tv/v0/subjects/${encodeURIComponent(animeId)}`, { signal: AbortSignal.timeout(5500) });
      if (res.ok) {
        const raw = (await res.json()) as Record<string, unknown>;
        const mapped = mapBangumiSubject(raw);
        if (mapped) base = mapped;
        if (typeof raw.summary === "string" && raw.summary.trim()) summary = raw.summary.trim();
      }
    } catch {
      // keep fallback
    }
    return {
      anime: base,
      titles: [base.title],
      summary,
      intro: summary,
      episodes: Array.from({ length: Math.min(base.totalEpisodes ?? 12, 12) }, (_, i) => ({
        episodeId: `${animeId}-ep-${i + 1}`,
        title: `第 ${i + 1} 话`,
        episodeNumber: String(i + 1),
        airDate: base.airDate ?? null
      })),
      mikanKeyword: base.title,
      downloads: [
        {
          id: `demo-res-${animeId}-1`,
          providerId: "mikan",
          providerIds: ["mikan"],
          title: `[LoliHouse] ${base.title} - 01 [WebRip 1080p HEVC-10bit AAC 简繁内封]`,
          subgroup: "LoliHouse",
          magnetUrl: `magnet:?xt=urn:btih:${animeId.padStart(40, "0").slice(0, 40)}`,
          torrentUrl: null,
          pageUrl: "https://mikanani.me",
          sizeText: "642.5 MB",
          publishedAt: nowIso(),
          sourceType: "magnet",
          resourceKey: `demo-key-${animeId}-1`,
          infoHash: animeId.padStart(40, "0").slice(0, 40),
          sizeBytes: 673710080,
          seeders: 58,
          leechers: 4
        }
      ],
      tags,
      rank: base.rank ?? null,
      totalEpisodes: base.totalEpisodes ?? 12,
      providerStatuses: [{ providerId: "mikan", displayName: "蜜柑计划", ok: true, count: 1, error: null }],
      platforms: [
        {
          id: "bangumi",
          displayName: "Bangumi",
          url: `https://bgm.tv/subject/${animeId}`,
          score: base.rating,
          rank: base.rank ?? null,
          scoreMax: 10
        }
      ],
      platformFailures: []
    };
  }

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const rawUrl = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(rawUrl, window.location.origin);
    if (!url.pathname.startsWith("/api/")) {
      return realFetch(input, init);
    }

    const method = (init?.method ?? "GET").toUpperCase();
    const path = url.pathname;
    const parseBody = (): Record<string, unknown> => {
      if (typeof init?.body === "string" && init.body) {
        try {
          return JSON.parse(init.body) as Record<string, unknown>;
        } catch {
          return {};
        }
      }
      return {};
    };

    if (path === "/api/bootstrap") {
      return jsonResponse({
        auth: {
          authRequired: true,
          authenticated: true,
          username: "demo",
          authMethod: "session",
          passwordConfigured: true,
          githubOAuthAvailable: false,
          googleOAuthAvailable: false,
          dianaAuthAvailable: false,
          dianaOwnerPairAvailable: false,
          oidcAvailable: false,
          oidcDisplayName: "SSO"
        },
        status: buildStatus(),
        settings: demoSettings,
        favorites,
        hidden: hiddenAnime,
        subscriptions,
        downloads,
        homepage: await fetchDemoHome(),
        homepageFresh: true
      });
    }
    if (path === "/api/auth/status") {
      return jsonResponse({
        authRequired: true,
        authenticated: true,
        username: "demo",
        authMethod: "session",
        passwordConfigured: true,
        githubOAuthAvailable: false,
        googleOAuthAvailable: false,
        dianaAuthAvailable: false,
        dianaOwnerPairAvailable: false,
        oidcAvailable: false,
        oidcDisplayName: "SSO"
      });
    }
    if (path === "/api/system/status") {
      return jsonResponse({
        ok: true,
        appVersion: "0.2.2",
        githubOAuthAvailable: false,
        googleOAuthAvailable: false,
        dianaAuthAvailable: false,
        dianaOwnerPairAvailable: false,
        oidcAvailable: false,
        oidcDisplayName: "SSO"
      });
    }
    if (path === "/api/system/status/full") {
      return jsonResponse(buildStatus());
    }
    if (path === "/api/auth/qr-ticket") {
      return jsonResponse({ code: "demo-qr-ticket", username: "demo", expiresInSeconds: 600 });
    }
    if (path === "/api/auth/sessions") {
      return jsonResponse([
        {
          id: 1,
          deviceName: "Web 在线演示会话",
          userAgent: navigator.userAgent,
          ipAddress: "127.0.0.1",
          authMethod: "password",
          githubLogin: "",
          githubId: "",
          createdAt: nowIso(),
          lastSeenAt: nowIso(),
          expiresAt: new Date(Date.now() + 30 * 86400_000).toISOString(),
          current: true
        }
      ]);
    }
    if (path === "/api/settings") {
      if (method === "PUT") {
        demoSettings = { ...demoSettings, ...(parseBody() as Partial<AppSettings>) };
      }
      return jsonResponse(demoSettings);
    }
    if (path === "/api/discovery/home") {
      return jsonResponse(await fetchDemoHome());
    }
    if (path === "/api/discovery/catalog") {
      return jsonResponse(await fetchDemoCatalog(url));
    }
    if (path === "/api/discovery/search") {
      const q = url.searchParams.get("q") ?? "";
      const catalog = await fetchDemoCatalog(new URL(`${window.location.origin}/api/discovery/catalog?keyword=${encodeURIComponent(q)}`));
      return jsonResponse({ items: catalog.items, platformFailures: [] });
    }
    if (path.startsWith("/api/discovery/streaming/")) {
      return jsonResponse({ streamingLinks: [] });
    }
    if (path.startsWith("/api/discovery/anime/")) {
      const id = decodeURIComponent(path.slice("/api/discovery/anime/".length));
      return jsonResponse(await fetchDemoDetail(id));
    }
    if (path === "/api/discovery/providers") {
      return jsonResponse([
        { id: "mikan", displayName: "蜜柑计划", enabled: true, configured: true, baseUrl: "https://mikanani.me" },
        { id: "animegarden", displayName: "AnimeGarden", enabled: true, configured: true, baseUrl: "https://garden.onekuma.cn" },
        { id: "nyaa", displayName: "Nyaa", enabled: true, configured: true, baseUrl: "https://nyaa.si" }
      ]);
    }
    if (path === "/api/discovery/metadata-platforms") {
      return jsonResponse([
        { id: "bangumi", displayName: "Bangumi 番组计划", enabled: true, requiresKey: false, keyConfigured: true },
        { id: "anilist", displayName: "AniList", enabled: true, requiresKey: false, keyConfigured: true }
      ]);
    }
    if (path === "/api/discovery/hidden") {
      if (method === "POST") {
        const b = parseBody();
        const anime = (b.anime as Partial<HomepageAnime> | undefined) ?? {};
        const item: HiddenAnime = {
          ...(FALLBACK_CATALOG_ANIME[0]),
          ...anime,
          animeId: String(anime.animeId ?? Date.now()),
          title: String(anime.title ?? "已隐藏番剧"),
          hiddenAt: nowIso()
        };
        hiddenAnime = [item, ...hiddenAnime.filter((x) => x.animeId !== item.animeId)];
        return jsonResponse(item);
      }
      if (method === "DELETE") {
        const count = hiddenAnime.length;
        hiddenAnime = [];
        return jsonResponse({ deletedCount: count });
      }
      return jsonResponse(hiddenAnime);
    }
    if (path.startsWith("/api/discovery/hidden/") && method === "DELETE") {
      const id = decodeURIComponent(path.slice("/api/discovery/hidden/".length));
      hiddenAnime = hiddenAnime.filter((x) => x.animeId !== id);
      return jsonResponse({ deleted: true });
    }
    if (path === "/api/favorites") {
      if (method === "POST") {
        const b = parseBody();
        const item: FavoriteAnime = {
          ...(FALLBACK_CATALOG_ANIME[0]),
          animeId: String(b.animeId ?? Date.now()),
          title: String(b.title ?? "收藏番剧"),
          imageUrl: (b.imageUrl as string | null) ?? null,
          rating: typeof b.rating === "number" ? b.rating : null,
          mikanKeyword: String(b.mikanKeyword ?? b.title ?? ""),
          rssUrl: null,
          subscriptionId: null,
          subscriptionName: null,
          createdAt: nowIso(),
          updatedAt: nowIso(),
          subscriptionAutoDownload: null,
          subscriptions: []
        };
        favorites = [item, ...favorites.filter((x) => x.animeId !== item.animeId)];
        return jsonResponse(item);
      }
      return jsonResponse(favorites);
    }
    if (path === "/api/favorites/subscribe" && method === "POST") {
      const b = parseBody();
      const anime = (b.anime as Record<string, unknown> | undefined) ?? {};
      const opts = (b.options as Record<string, unknown> | undefined) ?? {};
      const providerIds = Array.isArray(b.providerIds) ? (b.providerIds as string[]) : ["mikan"];
      const subgroup = typeof opts.subgroup === "string" && opts.subgroup !== "all" ? opts.subgroup : null;
      const rule: Subscription["rule"] = {
        includeKeywords: [],
        excludeKeywords: [],
        preferredResolutions: opts.resolution === "2160p" ? ["2160p", "1080p"] : opts.resolution === "any" ? [] : ["1080p"],
        preferredSubgroups: subgroup ? [subgroup] : [],
        lockedSubgroup: subgroup,
        preferredLanguage: (opts.language as Subscription["rule"]["preferredLanguage"]) ?? "chs",
        onlyNew: opts.onlyNew !== false,
        subscribedAt: nowIso(),
        baselineEpisode: typeof opts.baselineEpisode === "number" ? opts.baselineEpisode : 0,
        autoDownload: true,
        skipBatch: true
      };
      const animeId = String(anime.animeId ?? Date.now());
      const animeTitle = String(anime.title ?? "订阅番剧");
      subscriptions = subscriptions.filter((s) => s.animeId !== animeId);
      const createdList: Subscription[] = providerIds.map((pid, idx) => ({
        id: Date.now() + idx,
        name: `${animeTitle} · ${pid}`,
        url: `https://mikanani.me/RSS/Search?searchstr=${encodeURIComponent(animeTitle)}`,
        enabled: true,
        downloadDirOverride: null,
        rule,
        lastFetchedAt: nowIso(),
        lastError: null,
        createdAt: nowIso(),
        updatedAt: nowIso(),
        downloadedEpisodes: [],
        animeId,
        providerId: pid
      }));
      subscriptions = [...createdList, ...subscriptions];
      const favItem: FavoriteAnime = {
        ...(FALLBACK_CATALOG_ANIME[0]),
        ...(anime as Partial<FavoriteAnime>),
        animeId,
        title: animeTitle,
        mikanKeyword: String(b.mikanKeyword ?? animeTitle),
        rssUrl: createdList[0]?.url ?? null,
        subscriptionId: createdList[0]?.id ?? null,
        subscriptionName: createdList[0]?.name ?? null,
        createdAt: nowIso(),
        updatedAt: nowIso(),
        subscriptionAutoDownload: true,
        subscriptionRule: rule,
        subscriptions: createdList.map((s) => ({
          id: s.id,
          providerId: s.providerId,
          providerName: s.providerId,
          name: s.name,
          url: s.url,
          enabled: true,
          autoDownload: true
        }))
      };
      favorites = [favItem, ...favorites.filter((x) => x.animeId !== animeId)];
      return jsonResponse(createdList);
    }
    if (path.startsWith("/api/favorites/") && path.endsWith("/subscriptions") && method === "DELETE") {
      const id = decodeURIComponent(path.slice("/api/favorites/".length, -"/subscriptions".length));
      const before = subscriptions.length;
      subscriptions = subscriptions.filter((s) => s.animeId !== id);
      favorites = favorites.map((f) =>
        f.animeId === id
          ? { ...f, subscriptionId: null, subscriptionName: null, subscriptionAutoDownload: null, subscriptionRule: null, subscriptions: [] }
          : f
      );
      return jsonResponse({ deletedCount: before - subscriptions.length });
    }
    if (path.startsWith("/api/favorites/")) {
      const id = decodeURIComponent(path.slice("/api/favorites/".length));
      if (method === "DELETE") {
        favorites = favorites.filter((x) => x.animeId !== id);
        return jsonResponse({ deleted: true });
      }
    }
    if (path === "/api/subscriptions") {
      if (method === "POST") {
        const b = parseBody();
        const created: Subscription = {
          id: subscriptions.length + 1,
          name: String(b.name ?? "新订阅"),
          url: String(b.url ?? "https://mikanani.me/RSS/Search?searchstr=demo"),
          enabled: true,
          downloadDirOverride: null,
          rule: (b.rule as Subscription["rule"]) ?? subscriptions[0].rule,
          lastFetchedAt: nowIso(),
          lastError: null,
          createdAt: nowIso(),
          updatedAt: nowIso(),
          downloadedEpisodes: [],
          animeId: (b.animeId as string | null) ?? null,
          providerId: "mikan"
        };
        subscriptions = [created, ...subscriptions];
        return jsonResponse(created);
      }
      return jsonResponse(subscriptions);
    }
    if (path === "/api/feed-items") {
      return jsonResponse(feedItems);
    }
    if (path === "/api/downloads") {
      if (method === "POST") {
        const b = parseBody();
        const created: DownloadJob = {
          id: downloads.length + 1,
          feedItemId: 200 + downloads.length,
          gid: `native-demo-${Date.now()}`,
          sourceUrl: String(b.sourceUrl ?? ""),
          feedItemTitle: String(b.title ?? "演示下载任务 [1080p].mp4"),
          status: "active",
          totalLength: 520_000_000,
          completedLength: 82_000_000,
          downloadSpeed: 11_400_000,
          uploadSpeed: 0,
          errorMessage: null,
          saveTo: demoSettings.defaultSaveTo,
          createdAt: nowIso(),
          updatedAt: nowIso()
        };
        downloads = [created, ...downloads];
        return jsonResponse(created);
      }
      return jsonResponse(downloads);
    }
    if (path === "/api/logs/recent") {
      return jsonResponse(logs);
    }
    if (path === "/api/logs/file-info") {
      return jsonResponse({ path: "~/Library/Application Support/Bangumi Cat/server/app.db" });
    }
    if (path === "/api/system/storage") {
      return jsonResponse({
        dataDir: "~/.bgmcat",
        downloadDir: "~/.bgmcat/downloads",
        diskTotalBytes: 512_000_000_000,
        diskFreeBytes: 184_500_000_000,
        appBytes: 1_162_528_400,
        appFiles: 12,
        cacheBytes: 18_420_000,
        directories: [
          { key: "downloads", label: "番剧下载", path: "~/.bgmcat/downloads", bytes: 1_140_708_400, files: 4 },
          { key: "database", label: "数据库", path: "~/.bgmcat/app.db", bytes: 1_820_000, files: 1 },
          { key: "cache", label: "播放与缩略图缓存", path: "~/.bgmcat/cache", bytes: 18_420_000, files: 5 },
          { key: "state", label: "做种与任务状态", path: "~/.bgmcat/state", bytes: 1_580_000, files: 2 },
          { key: "backups", label: "数据备份", path: "~/.bgmcat/backups", bytes: 0, files: 0 }
        ]
      });
    }
    if (path === "/api/cloud/providers") {
      return jsonResponse([
        { provider: "google", displayName: "Google Drive", authKind: "oauth", connected: true, configured: true, folderId: demoSettings.cloudDriveFolderId },
        { provider: "onedrive", displayName: "Microsoft OneDrive", authKind: "oauth", connected: false, configured: false, folderId: "root" },
        { provider: "webdav", displayName: "WebDAV", authKind: "basic", connected: false, configured: false, folderId: "Bangumi Cat" }
      ]);
    }
    if (path === "/api/cloud/status") {
      const p = url.searchParams.get("provider") || demoSettings.cloudProvider || "google";
      return jsonResponse({
        provider: p,
        displayName: p === "onedrive" ? "Microsoft OneDrive" : p === "webdav" ? "WebDAV" : "Google Drive",
        connected: p === "google",
        configured: p === "google"
      });
    }
    // 演示没有真实文件：播放信息指向官网上的一段测试视频，见 api.ts 的 getDownloadStreamUrl。
    const playbackMatch = /^\/api\/downloads\/([^/]+)\/playback(\/progress)?$/.exec(path);
    if (playbackMatch) {
      const gid = decodeURIComponent(playbackMatch[1]);
      if (playbackMatch[2]) {
        return jsonResponse({ gid, deliveryMode: "direct", bufferedSeconds: 10, durationSeconds: 10, finished: true, error: null });
      }
      const job = downloads.find((item) => item.gid === gid);
      const fileName = `${job?.feedItemTitle?.replace(/[\\/]/g, " ").trim() || "演示视频"}.mp4`;
      return jsonResponse({
        gid,
        filePath: `${demoSettings.defaultSaveTo}/${fileName}`,
        fileName,
        mimeType: "video/mp4",
        durationSeconds: 10,
        deliveryMode: "direct",
        supportsInAppPlayback: true,
        supportsTranscodePlayback: false,
        subtitleTracks: [],
        availablePlayers: []
      });
    }
    if (path === "/api/downloads/engines/test") {
      const b = parseBody();
      const c = String(b.client || demoSettings.downloadClient || "native");
      return jsonResponse({
        client: c,
        displayName: c === "qbittorrent" ? "qBittorrent" : c === "aria2-rpc" ? "Aria2 RPC" : c === "transmission" ? "Transmission" : "内置引擎",
        ok: c === "native",
        version: c === "native" ? "Pure-Go BT/HTTP" : "",
        error: c === "native" ? "" : "演示模式未连接本地客户端",
        latencyMs: 3
      });
    }
    if (path === "/api/network/targets") {
      return jsonResponse([
        { id: "google", name: "Google", group: "cloud", hosts: ["www.googleapis.com", "drive.usercontent.google.com"], custom: false, enabled: true, selectedIP: "142.250.196.110", availableCount: 2, totalCount: 2, fastestMs: 42 },
        { id: "mikan", name: "蜜柑计划", group: "source", hosts: ["mikanani.me", "mikanime.tv"], custom: false, enabled: false, selectedIP: "", availableCount: 0, totalCount: 0 },
        { id: "dmhy", name: "动漫花园", group: "source", hosts: ["share.dmhy.org"], custom: false, enabled: false, selectedIP: "", availableCount: 0, totalCount: 0 },
        { id: "nyaa", name: "Nyaa", group: "source", hosts: ["nyaa.si"], custom: false, enabled: false, selectedIP: "", availableCount: 0, totalCount: 0 },
        { id: "onedrive", name: "Microsoft OneDrive", group: "cloud", hosts: ["graph.microsoft.com", "login.microsoftonline.com"], custom: false, enabled: false, selectedIP: "", availableCount: 0, totalCount: 0 },
        { id: "tmdb", name: "TMDB", group: "metadata", hosts: ["api.themoviedb.org"], custom: false, enabled: false, selectedIP: "", availableCount: 0, totalCount: 0 }
      ]);
    }
    if (path.startsWith("/api/network/targets/")) {
      const id = decodeURIComponent(path.slice("/api/network/targets/".length).split("/")[0] || "google");
      return jsonResponse({
        id,
        name: id === "mikan" ? "蜜柑计划" : id === "onedrive" ? "Microsoft OneDrive" : "Google",
        group: id === "mikan" ? "source" : "cloud",
        hosts: id === "mikan" ? ["mikanani.me", "mikanime.tv"] : ["www.googleapis.com", "drive.usercontent.google.com"],
        custom: false,
        enabled: id === "google",
        bypassTun: true,
        interface: "en0",
        sniMode: "original",
        sniHost: "",
        preferredIP: id === "google" ? "142.250.196.110" : "",
        selectedIP: id === "google" ? "142.250.196.110" : "",
        results: id === "google"
          ? [
              { ip: "142.250.196.110", available: true, tcpLatencyMs: 28, latencyMs: 42, verifiedAt: Math.floor(Date.now() / 1000), warning: "", error: "", sni: "fragment" },
              { ip: "172.217.160.78", available: true, tcpLatencyMs: 35, latencyMs: 58, verifiedAt: Math.floor(Date.now() / 1000), warning: "", error: "", sni: "fragment" }
            ]
          : []
      });
    }
    if (path === "/api/files/local/content" || path === "/api/files/cloud/content") {
      return new Response("[Script Info]\nTitle: Demo Preview\nOriginal Script: Bangumi Cat", {
        status: 200,
        headers: { "Content-Type": "text/plain; charset=utf-8" }
      });
    }
    if (path === "/api/files/local/trash/restore") {
      return jsonResponse({ restored: true, path: "ep01.mp4" });
    }
    if (path === "/api/files/local/trash/empty") {
      return jsonResponse({ deletedCount: 1, deletedBytes: 128_400 });
    }
    if (path === "/api/files/local") {
      if (method === "DELETE") {
        return jsonResponse({ deleted: true, trashed: url.searchParams.get("permanent") !== "1", path: url.searchParams.get("path") ?? "" });
      }
      const sub = url.searchParams.get("path") ?? "";
      return jsonResponse({
        rootPath: demoSettings.downloadDir,
        path: sub,
        freeBytes: 184_500_000_000,
        totalBytes: 512_000_000_000,
        trashCount: 0,
        trashBytes: 0,
        entries: sub === ".trash"
          ? []
          : sub
            ? [
                { id: `local:${sub}/01.mkv`, name: "[LoliHouse] 葬送的芙莉莲 - 01 [WebRip 1080p HEVC-10bit AAC].mkv", path: `${sub}/01.mkv`, isDir: false, size: 642_180_000, modTime: nowIso(), ext: "mkv", kind: "video", playable: true, mediaId: "demo-gid-1" },
                { id: `local:${sub}/01.sc.ass`, name: "[LoliHouse] 葬送的芙莉莲 - 01 [WebRip 1080p HEVC-10bit AAC].sc.ass", path: `${sub}/01.sc.ass`, isDir: false, size: 128_400, modTime: nowIso(), ext: "ass", kind: "subtitle", playable: false, mediaId: "" }
              ]
            : [
                { id: "local:葬送的芙莉莲", name: "葬送的芙莉莲", path: "葬送的芙莉莲", isDir: true, size: 0, modTime: nowIso(), ext: "", kind: "folder", playable: false, mediaId: "" },
                { id: "local:败犬女主太多了！", name: "败犬女主太多了！", path: "败犬女主太多了！", isDir: true, size: 0, modTime: nowIso(), ext: "", kind: "folder", playable: false, mediaId: "" },
                { id: "local:ep28.mp4", name: "[北宇治字幕组] 葬送的芙莉莲 [28][WebRip][HEVC_AAC][简繁日内封].mp4", path: "ep28.mp4", isDir: false, size: 518_400_000, modTime: nowIso(), ext: "mp4", kind: "video", playable: true, mediaId: "demo-gid-2" }
              ]
      });
    }
    if (path === "/api/files/local/mkdir") {
      const b = parseBody();
      const name = String(b.name ?? "新文件夹");
      const parent = String(b.parent ?? "");
      return jsonResponse({ name, path: parent ? `${parent}/${name}` : name });
    }
    if (path === "/api/files/cloud") {
      const parent = url.searchParams.get("parent") || demoSettings.cloudDriveFolderId || "root";
      return jsonResponse({
        connected: true,
        configured: true,
        archiveFolderId: demoSettings.cloudDriveFolderId,
        parent,
        parentName: parent === "root" ? "我的云盘" : parent === demoSettings.cloudDriveFolderId ? "Bangumi Cat 番剧库" : "Season 1",
        quota: { usageBytes: 48_318_382_080, limitBytes: 214_748_364_800, userEmail: "bgmcat@gmail.com" },
        entries:
          parent === "folder_frieren"
            ? [
                { id: "cloud_ep01", name: "[LoliHouse] 葬送的芙莉莲 - 01 [1080p].mp4", path: "cloud_ep01", isDir: false, mimeType: "video/mp4", size: 580_120_000, modTime: nowIso(), ext: "mp4", kind: "video", playable: true, mediaId: "demo-cloud-1" },
                { id: "cloud_ep02", name: "[LoliHouse] 葬送的芙莉莲 - 02 [1080p].mp4", path: "cloud_ep02", isDir: false, mimeType: "video/mp4", size: 564_800_000, modTime: nowIso(), ext: "mp4", kind: "video", playable: true, mediaId: "demo-cloud-2" }
              ]
            : [
                { id: "folder_frieren", name: "葬送的芙莉莲 (2023)", path: "folder_frieren", isDir: true, mimeType: "application/vnd.google-apps.folder", size: 0, modTime: nowIso(), ext: "", kind: "folder", playable: false, mediaId: "" },
                { id: "folder_makeine", name: "败犬女主太多了！ (2024)", path: "folder_makeine", isDir: true, mimeType: "application/vnd.google-apps.folder", size: 0, modTime: nowIso(), ext: "", kind: "folder", playable: false, mediaId: "" },
                { id: "cloud_movie", name: "[喵萌奶茶屋] 孤独摇滚！剧场总集篇 Re: [1080p][简日内嵌].mp4", path: "cloud_movie", isDir: false, mimeType: "video/mp4", size: 1_842_000_000, modTime: nowIso(), ext: "mp4", kind: "video", playable: true, mediaId: "demo-cloud-3" }
              ]
      });
    }
    if (path === "/api/files/cloud/mkdir") {
      const b = parseBody();
      return jsonResponse({ id: `folder_${Date.now()}`, name: String(b.name ?? "新文件夹") });
    }
    if (path.startsWith("/api/files/cloud/") && method === "DELETE") {
      return jsonResponse({ deleted: true, trashed: true });
    }
    if (path === "/api/cloud/network") {
      return jsonResponse({
        enabled: true,
        bypassTun: false,
        sniMode: "original",
        sniHost: "",
        preferredIP: "142.250.196.110",
        selectedIP: "142.250.196.110",
        results: [
          { ip: "142.250.196.110", ok: true, apiOk: true, downloadOk: true, latencyMs: 42, verifiedAt: nowIso(), error: "" },
          { ip: "172.217.160.78", ok: true, apiOk: true, downloadOk: true, latencyMs: 58, verifiedAt: nowIso(), error: "" }
        ]
      });
    }
    if (path === "/api/tunnel/status") {
      return jsonResponse({
        enabled: demoSettings.tunnelEnabled,
        state: demoSettings.tunnelEnabled ? "online" : "stopped",
        mode: "quick",
        publicUrl: demoSettings.tunnelEnabled ? "https://demo-cat.trycloudflare.com" : null,
        readyConnections: demoSettings.tunnelEnabled ? 4 : 0,
        binaryFound: true,
        lastError: null
      });
    }
    if (path === "/api/tunnel/preflight" || path === "/api/tunnel/logs" || path === "/api/tunnel/discovered") {
      return jsonResponse([]);
    }
    if (path === "/api/agents/detect") {
      return jsonResponse([{ host: "diana", displayName: "Diana", detected: true, installed: true, configPath: "~/.diana/data/.mcp.json" }]);
    }
    if (path === "/api/local-ai/status") {
      return jsonResponse({ enabled: false, baseUrl: "http://127.0.0.1:11434", model: "gemma3:4b", reachable: false, availableModels: [], lastError: null });
    }
    if (path === "/api/system/version") {
      return jsonResponse({ currentVersion: "0.2.2", channel: "stable" });
    }
    if (path === "/api/system/update" || path === "/api/system/update/check") {
      return jsonResponse({ state: "up-to-date", currentVersion: "0.2.2", latestVersion: "0.2.2", releaseUrl: "https://github.com/SuInk/bgmcat/releases" });
    }
    if (path === "/api/system/update/policy") {
      return jsonResponse({ autoDownload: true, autoInstall: false, mirror: "auto", channel: "stable" });
    }
    if (path === "/api/system/update/changelog") {
      return jsonResponse([]);
    }
    if (path === "/api/system/backups") {
      return jsonResponse({ backups: [] });
    }
    if (path === "/api/shares") {
      if (method === "POST") {
        const b = parseBody();
        const code = `demo_${Math.random().toString(36).slice(2, 10)}`;
        const passcode = String(b.passcode ?? "").trim();
        const hrs = Number(b.expiresInHours ?? 168);
        return jsonResponse({
          code,
          title: String(b.title || "葬送的芙莉莲"),
          hasPasscode: Boolean(passcode),
          passcode,
          allowDownload: b.allowDownload !== false,
          expiresAt: hrs > 0 ? new Date(Date.now() + hrs * 3600_000).toISOString() : null,
          expired: false,
          createdAt: nowIso(),
          viewCount: 0,
          itemCount: 1,
          playableCount: 1,
          totalSize: 518_400_000,
          items: [{ id: "0", label: "第 01 集", fileName: "01.mp4", ext: "mp4", kind: "video", size: 518_400_000, playable: true }]
        });
      }
      if (method === "DELETE") {
        return jsonResponse({ removedCount: 0 });
      }
      return jsonResponse([]);
    }
    if (path.startsWith("/api/shares/")) {
      if (method === "DELETE") {
        return jsonResponse({ deleted: true, code: path.slice("/api/shares/".length) });
      }
    }

    return jsonResponse({});
  };
}
