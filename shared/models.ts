export type SourceType = "direct" | "magnet" | "torrent-url";
export type DownloadClientType = "native" | "qbittorrent";
export type MikanDirectAccessMode = "prefer" | "off";
export type HomepageSortMode = "default" | "rating";
export type TunnelMode = "quick" | "named";
export type TunnelCredentialMode = "credentials-file" | "token";
export type TunnelState = "stopped" | "starting" | "online" | "degraded" | "error";

/** 默认主题色：图标里的电光蓝。--accent 由它 color-mix 压暗得到图标深蓝。 */
export const DEFAULT_THEME_ACCENT = "#0041fd";

export interface AppSettings {
 dianaMcpConfigPath: string;
  bindHost: string;
  port: number;
  token: string;
  corsOrigins: string[];
  pollIntervalMinutes: number;
  downloadDir: string;
  cloudArchiveEnabled: boolean;
  googleClientId: string;
  googleClientSecret: string;
  cloudDriveFolderId: string;
  googleRedirectUri: string;
  cloudDeleteLocal: boolean;

  downloadClient: DownloadClientType;
  aria2RpcHost: string;
  aria2RpcPort: number;
  aria2RpcSecret: string;
  aria2SessionPath: string;
  aria2Enabled: boolean;
  aria2SeedEnabled: boolean;
  /** 额外的公共 Tracker，下载磁力 / 种子时附加给 aria2（与种子自带的并用） */
  btTrackers: string[];
  /** 新任务默认保存到本机还是 Google Drive（订阅自动下载、详情页一键下载用） */
  defaultSaveTo: "local" | "cloud";
  qbittorrentBaseUrl: string;
  qbittorrentUsername: string;
  qbittorrentPassword: string;
  qbittorrentEnabled: boolean;
  mikanBaseUrl: string;
  mikanDirectAccessMode: MikanDirectAccessMode;
  /** @deprecated Use mikanDirectAccessMode instead. */
  mikanDirectAccessEnabled: boolean;
  mikanResolverIps: string[];
  localAiEnabled: boolean;
  localAiBaseUrl: string;
  localAiModel: string;
  localAiSystemPrompt: string;
  homepageSortMode: HomepageSortMode;
  /**
   * @deprecated 已废弃。番剧详情改成接管整个内容区后不再有「第三级资源侧栏」，
   * 前端不再读这个字段。保留只为兼容旧配置与旧数据库（删列要迁移，不值当）。
   */
  multiSidebarEnabled: boolean;
  tunnelEnabled: boolean;
  tunnelMode: TunnelMode;
  tunnelBinaryPath: string;
  tunnelAutoRestart: boolean;
  tunnelHostname: string;
  tunnelCredentialMode: TunnelCredentialMode;
  tunnelCredentialsFile: string;
  tunnelId: string;
  tunnelToken: string;
  exposeMcpPublicly: boolean;
  githubOAuthEnabled: boolean;
  githubClientId: string;
  githubClientSecret: string;
  /** GitHub 用户名白名单,空数组表示谁都登不进(默认)。比对忽略大小写。 */
  githubAllowedLogins: string[];
  /** 覆盖内置 ffmpeg 的可执行文件路径；留空用内置产物，内置缺失时回落到 PATH */
  ffmpegPath: string;
  /** 发现新版本后自动下载并校验（不安装）。默认开。 */
  updateAutoDownload: boolean;
  /** 下载完成后自动备份、替换并重启。默认关：换版本要用户点头。 */
  updateAutoInstall: boolean;
  /** 下载加速策略：auto（实测挑线路）、direct（始终直连），或一条具体的镜像地址。 */
  updateMirror: string;
  /** 更新通道。目前只有 stable，字段先留着，免得将来加 beta 又要改一遍五处。 */
  updateChannel: string;
  /** 开机自启（mac/win 用 setLoginItemSettings，Linux 写 ~/.config/autostart） */
  launchAtLogin: boolean;
  /** 关闭窗口时隐藏到托盘继续后台下载，而不是退出应用 */
  minimizeToTray: boolean;
  /**
   * 启用的资源来源 id 列表。默认只有 mikan：没实测通的源不该默认打开，
   * 否则用户第一次打开详情页就看到一片「该源暂时不可用」。
   */
  enabledResourceProviders: string[];
  /** 资源来源的显示/择优顺序；不在列表里的源排在最后。 */
  resourceProviderOrder: string[];
  /**
   * 启用的信息平台 id 列表（bangumi / anilist / mal / tmdb）。
   * 默认只开 bangumi 与 anilist——这两个实测无需鉴权即可达；
   * MAL 走 Jikan 时常回源失败，TMDB 要 key，都交给用户自己开并用「测试」确认。
   */
  enabledMetadataPlatforms: string[];
  /** TMDB API key。空字符串表示没配，此时 TMDB 平台一律跳过，不发请求。 */
  tmdbApiKey: string;
  /** 用户选的主题色（#rrggbb）。前端把它写成根元素的 --accent-seed，其余色由 color-mix 派生。 */
  themeAccent: string;
  /**
   * 被环境变量接管、因而在设置页改不动的字段名（容器部署常见）。
   * 只读、不落库：由服务端每次读设置时现算。
   */
  lockedSettings?: string[];
}

export interface TunnelStatusSnapshot {
  state: TunnelState;
  enabled: boolean;
  mode: TunnelMode;
  binaryPath: string | null;
  binarySource: "override" | "bundled" | "path" | null;
  binaryAvailable: boolean;
  publicUrl: string | null;
  hostname: string | null;
  connectorId: string | null;
  readyConnections: number;
  metricsPort: number | null;
  pid: number | null;
  startedAt: string | null;
  restartCount: number;
  nextRetryAt: string | null;
  lastError: string | null;
}

export interface TunnelLogEntry {
  id: number;
  level: "info" | "warn" | "error";
  message: string;
  createdAt: string;
}

export interface TunnelPreflightIssue {
  level: "block" | "warn";
  code: string;
  message: string;
  autoFixable: boolean;
}

export interface DiscoveredTunnel {
  uuid: string;
  hostname: string | null;
  credentialsFile: string;
  /** 该条目是否来自用户 ~/.cloudflared/config.yml 的 ingress 定义。 */
  fromConfig: boolean;
}

export interface SystemStatus {
  appVersion: string;
  activeHost: string;
  activePort: number;
  configuredHost: string;
  configuredPort: number;
  apiBaseUrl: string;
  mcpSseUrl: string;
  tokenHeaderName: string;
  tokenConfigured: boolean;
  localAuthBypassEnabled: boolean;
  aria2Online: boolean;
  qbittorrentOnline: boolean;
  downloadClient: DownloadClientType;
  rssPollingEnabled: boolean;
  restartRequired: boolean;
  tunnelEnabled: boolean;
  tunnelState: TunnelState;
  tunnelPublicUrl: string | null;
  publicExposureActive: boolean;
}

export interface PublicSystemStatus {
  ok: boolean;
  appVersion: string;
  /** 是否可以走 GitHub 登录(已开启且配置了 client id),只是个布尔值。 */
  githubOAuthAvailable: boolean;
}

export interface AuthSessionSummary {
  id: number;
  githubLogin: string;
  githubId: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  current: boolean;
}

export interface GithubLoginResult {
  token: string;
  login: string;
  expiresAt: string;
}

export interface RuntimeConnectionInfo {
  appVersion: string;
  host: string;
  port: number;
  apiBaseUrl: string;
  mcpSseUrl: string;
  tokenHeaderName: string;
  token: string;
}

export interface LocalAiStatus {
  enabled: boolean;
  baseUrl: string;
  model: string;
  reachable: boolean;
  availableModels: string[];
  lastError: string | null;
}

export interface LocalAiChatMessage {
  role: "user" | "assistant";
  content: string;
}

export interface LocalAiChatResponse {
  message: LocalAiChatMessage;
}

export interface SubscriptionRule {
  includeKeywords: string[];
  excludeKeywords: string[];
  preferredResolutions: string[];
  autoDownload: boolean;
  /** 跳过合集/批量资源（一个 BDBOX 可能几十 GB）。默认 true。 */
  skipBatch: boolean;
  /**
   * 字幕组偏好，有序：越靠前越优先。跨源同集择优的第一优先级。
   * 与 preferredResolutions 一样是「排序偏好」不是硬过滤——
   * 列表里的字幕组都没发这一集时，照样下别人发的。
   */
  preferredSubgroups: string[];
}

export interface Subscription {
  id: number;
  name: string;
  url: string;
  enabled: boolean;
  downloadDirOverride: string | null;
  rule: SubscriptionRule;
  lastFetchedAt: string | null;
  lastError: string | null;
  createdAt: string;
  updatedAt: string;
  /** 派生只读字段：该订阅下已下载完成的集数。 */
  downloadedEpisodes: number[];
  /** 这条订阅追的是哪部番（bangumi subject id）。手动加的杂源订阅为 null。 */
  animeId: string | null;
  /** 这条订阅来自哪个资源源。认不出来源为 null。 */
  providerId: string | null;
}

export interface FeedItem {
  id: number;
  subscriptionId: number | null;
  title: string;
  imageUrl?: string | null;
  guid: string | null;
  link: string | null;
  publishedAt: string | null;
  sourceType: SourceType | null;
  sourceUrl: string | null;
  infoHash: string | null;
  normalizedTitle: string;
  dedupeKey: string;
  matched: boolean;
  matchReason: string | null;
  downloadStatus: "pending" | "queued" | "complete" | "error" | "skipped" | "ignored";
  createdAt: string;
  /** 从标题解析出的集数；合集与解析失败为 null。 */
  episodeNumber: number | null;
  /** 是否为合集/批量资源。 */
  isBatch: boolean;
  /** 字幕组。 */
  subgroup: string | null;
  /** 归一化后的分辨率，如 "1080p"。 */
  resolution: string | null;
  /** 跨源标识：优先 infoHash，否则是归一化标题+集数+体积的指纹。 */
  resourceKey: string | null;
}

export interface CloudArchiveFile {
  localPath: string;
  remoteName: string;
  remoteId: string | null;
  size: number;
  md5: string;
}

export interface CloudArchive {
  status: "uploading" | "verified" | "archived" | "error";
  folderId: string;
  files: CloudArchiveFile[];
  error: string | null;
  retryAt: number;
}

export interface DownloadJob {
  cloudArchive?: CloudArchive | null;
  id: number;
  feedItemId: number;
  gid: string;
  feedItemTitle?: string | null;
  feedItemImageUrl?: string | null;
  feedItemGuid?: string | null;
  feedItemSubscriptionId?: number | null;
  feedItemLink?: string | null;
  feedItemSourceUrl?: string | null;
  thumbnailUrl?: string | null;
  status: "queued" | "active" | "paused" | "waiting" | "complete" | "error" | "removed";
  totalLength: number;
  completedLength: number;
  downloadSpeed: number;
  uploadSpeed: number;
  errorMessage: string | null;
  connectionCount?: number;
  errorCode?: string | null;
  issueKind?: "connected-idle" | "tracker-error" | "file-conflict" | null;
  issueMessage?: string | null;
  canResolveConflict?: boolean;
  canPlayExistingFile?: boolean;
  /** 保存位置：cloud 表示下载完成后上传 Google Drive，校验通过后删掉本机副本 */
  saveTo?: "local" | "cloud";
  /** 任务的文件清单；云盘归档时每个文件记着已上传的字节数 */
  files?: Array<{ path?: string; size?: number; uploadedBytes?: number; verified?: boolean }>;
  createdAt: string;
  updatedAt: string;
}

export type DownloadConflictStrategy = "overwrite" | "delete-existing" | "recheck";

export type ExternalPlayerId = "system" | "vlc" | "mpv" | "potplayer" | "mpc-hc";

export interface AvailableExternalPlayer {
  id: ExternalPlayerId;
  label: string;
}

export interface PlaybackSubtitleTrack {
  id: string;
  label: string;
  language: string | null;
  codec: string | null;
}

export type PlaybackDeliveryMode = "direct" | "hls-remux" | "hls-transcode" | "external";

export interface DownloadPlaybackInfo {
  gid: string;
  filePath: string;
  fileName: string;
  mimeType: string;
  durationSeconds: number | null;
  deliveryMode: PlaybackDeliveryMode;
  supportsInAppPlayback: boolean;
  supportsTranscodePlayback: boolean;
  subtitleTracks: PlaybackSubtitleTrack[];
  availablePlayers: AvailableExternalPlayer[];
}

export interface LaunchPlayerResult {
  gid: string;
  playerId: ExternalPlayerId;
  playerLabel: string;
  launched: boolean;
}

export interface LogEntry {
  id: number;
  level: "info" | "warn" | "error";
  message: string;
  createdAt: string;
}

export interface HomepageAnime {
  animeId: string;
  bangumiId: string | null;
  title: string;
  imageUrl: string | null;
  searchKeyword: string | null;
  airDay: number | null;
  isOnAir: boolean;
  isRestricted: boolean;
  rating: number | null;
  source: "bangumi";
  mikanId: string | null;
  officialSite: string | null;
  /** 首播时刻（ISO 瞬间，来自 bangumi-data 的 broadcast/begin）。星期与时间都由它推导。 */
  broadcastAt?: string | null;
  /**
   * 跨平台 id。来源优先是 bangumi-data 的 sites[]（权威对照），其次是各平台自报（如 AniList 的 idMal）。
   * 注意 source 字段仍固定为 "bangumi"——favorite_anime.source 列依赖它，不随聚合改动。
   */
  externalIds?: Partial<Record<"bangumi" | "anilist" | "mal" | "tmdb" | "anidb", string>>;
  /** 这条结果在哪些来源上命中。平台 id，外加本地 bangumi-data 命中时的 "bangumi-data"。 */
  matchedPlatforms?: string[];
}

export interface FavoriteAnime extends HomepageAnime {
  mikanKeyword: string;
  rssUrl: string | null;
  subscriptionId: number | null;
  subscriptionName: string | null;
  createdAt: string;
  updatedAt: string;
  /** 关联订阅是否开启自动下载。未订阅时为 null。用于区分「仅收集」和「自动追更」。 */
  subscriptionAutoDownload: boolean | null;
  /**
   * 该番挂着的全部订阅，一个源一条。
   * 上面的 subscriptionId / subscriptionName / subscriptionAutoDownload 是其中的「主订阅」
   * （按来源排序取第一条），保留是为了不让只认单条订阅的旧调用方一起改。
   */
  subscriptions: FavoriteAnimeSubscription[];
}

export interface FavoriteAnimeSubscription {
  id: number;
  providerId: string | null;
  providerName: string | null;
  name: string;
  url: string;
  enabled: boolean;
  autoDownload: boolean;
}

export interface AnimeEpisode {
  episodeId: string;
  title: string;
  episodeNumber: string | null;
  airDate: string | null;
}

export interface MikanDownloadItem {
  id: string;
  title: string;
  subgroup: string | null;
  magnetUrl: string | null;
  torrentUrl: string | null;
  pageUrl: string | null;
  sizeText: string | null;
  publishedAt: string | null;
  sourceType: SourceType;
}

export interface AnimeDetail {
  /** 基本信息已就绪，分集与下载资源仍在补充。 */
  detailPending?: boolean;
  anime: HomepageAnime;
  titles: string[];
  summary: string | null;
  intro: string | null;
  episodes: AnimeEpisode[];
  mikanKeyword: string;
  /** 跨源合并后的资源列表。ResourceDownloadItem 是 MikanDownloadItem 的超集。 */
  downloads: ResourceDownloadItem[];
  downloadError?: string | null;
  /** Bangumi 主题标签（按热度取前若干个） */
  tags?: string[];
  /** Bangumi 站内排名 */
  rank?: number | null;
  /** 总集数（Bangumi 主题数据） */
  totalEpisodes?: number | null;
  /** 每个资源源这一次抓取的结果。失败的源要显示出来，而不是静悄悄少几行。 */
  providerStatuses?: ResourceProviderFetchStatus[];
  /**
   * 各信息平台的评分与链接，**各自保留、绝不平均**——
   * Bangumi 0-10、AniList 0-100、MAL 0-10 的评分体系与人群都不可比。
   */
  platforms?: AnimeMetadataPlatform[];
  /** 这一轮没响应的信息平台。结果照出，前端浅色提示一行。 */
  platformFailures?: AnimeMetadataPlatformFailure[];
}

/**
 * 聚合搜索的返回。除了结果本身，还要告诉前端**哪个平台这一轮没响应**——
 * 结果照常显示，顶部浅色提示一行，而不是静悄悄少几条。
 */
export interface AnimeSearchResult {
  items: HomepageAnime[];
  platformFailures: AnimeMetadataPlatformFailure[];
}

/** 详情页「各平台评分」的一行。 */
export interface AnimeMetadataPlatform {
  id: string;
  displayName: string;
  url: string;
  score?: number | null;
  scoreCount?: number | null;
  rank?: number | null;
  /** 该平台的评分满分，前端据此决定怎么显示（AniList 是 100 分制）。 */
  scoreMax?: number;
}

/** 某个信息平台这一轮没能用上的原因。 */
export interface AnimeMetadataPlatformFailure {
  id: string;
  displayName: string;
  reason: string;
}

/** 跨源合并后的一条资源。是 MikanDownloadItem 的超集，前端原样消费。 */
export interface ResourceDownloadItem extends MikanDownloadItem {
  /** 展示所用的源（合并时按来源顺序取第一个）。 */
  providerId: string;
  /** 提供了这份资源的全部源。 */
  providerIds: string[];
  resourceKey: string;
  infoHash: string | null;
  sizeBytes: number | null;
  seeders: number | null;
  leechers: number | null;
}

export interface ResourceProviderFetchStatus {
  providerId: string;
  displayName: string;
  ok: boolean;
  count: number;
  error: string | null;
  /**
   * 这一轮还没回来（不是失败）。首屏会先把快的源的结果发出去，
   * 慢的源标成 pending 让前端提示「还在找」，回来之后再补上。
   */
  pending?: boolean;
}

/** 设置页「资源来源」列出的一项。 */
export interface ResourceProviderSummary {
  id: string;
  displayName: string;
  enabled: boolean;
}

/** release：CI 打出来的安装包，可以整包替换自己；source：源码部署，只能拉代码重建。 */
export type UpdateBuildType = "release" | "source";

/**
 * 更新状态机。这几个态必须互相区分，尤其是 unsupported 和 up_to_date——
 * 两者都没有可点的按钮，但含义相反：一个是「已经最新」，一个是「这台机器升不了级」，
 * 混在一起显示等于骗用户。
 */
export type UpdateStatusKind =
  | "unknown"
  /** 发布仓里还没有任何 release：和「已是最新」不是一回事。 */
  | "no_release"
  | "up_to_date"
  | "available"
  /** 有更新但现在装不了（工作区脏、非快进）。和 unsupported 的区别是它能被修好。 */
  | "blocked"
  | "downloading"
  | "downloaded"
  | "installing"
  | "restart_required"
  | "unsupported"
  | "error";

export interface UpdateReleaseSummary {
  tag: string;
  notes: string;
  publishedAt: string | null;
  prerelease: boolean;
}

export interface UpdateSourceStatus {
  /** 工作区有未提交改动：这种状态下拒绝更新，否则快进会把用户的改动冲掉。 */
  dirty: boolean;
  branch: string;
  headCommit: string;
  describe: string;
  /** 跟踪的远端分支，形如 origin/main；空串表示没有 origin，源码模式根本无从更新。 */
  upstream: string;
  /** 远端比本地多出的提交数。源码模式的「有没有更新」就是看它，而不是看 tag。 */
  behind: number;
  /** 本地比远端多出的提交数。非零通常意味着更新只能是非快进的。 */
  ahead: number;
  /** 远端不是本地的后代：只允许快进，这种情况必须拒绝。 */
  diverged: boolean;
  /** 远端最新的 v* tag，没有 tag 的仓库为空串。 */
  latestTag: string;
  /**
   * 非空表示「现在升不了级」，内容是给人看的原因（工作区脏、非快进、没有 origin）。
   * 它必须和「已是最新」分开显示：两者都没有可点的按钮，含义却相反。
   */
  blockedReason: string;
  /** 已经构建完新代码，等一次重启才会真正生效。 */
  restartRequired: boolean;
  /** 侦测到的进程管理器（launchd/systemd），空串表示没有，重启要用户自己来。 */
  serviceManager: string;
}

export interface UpdateProgress {
  phase: "idle" | "checking" | "checksum" | "downloading" | "staging" | "installing";
  done: number;
  total: number;
  percent: number;
}

export interface UpdateStatus {
  kind: UpdateStatusKind;
  currentVersion: string;
  buildType: UpdateBuildType;
  platform: string;
  arch: string;
  /** 能不能自更新；false 时 unsupportedReason 必须显示出来。 */
  supported: boolean;
  unsupportedReason: string;
  latestVersion: string;
  latestPublishedAt: string | null;
  updateAvailable: boolean;
  checkedAt: string | null;
  downloadedVersion: string;
  progress: UpdateProgress;
  busy: boolean;
  lastError: string;
  /** 同一版本连续安装失败的次数；达到 3 次后自动安装熔断。 */
  failureCount: number;
  circuitBroken: boolean;
  source: UpdateSourceStatus | null;
}

export interface UpdatePolicy {
  autoDownload: boolean;
  autoInstall: boolean;
  mirror: string;
  channel: string;
}

export interface UpdateVersionInfo {
  version: string;
  buildType: UpdateBuildType;
  platform: string;
  arch: string;
  gitDescribe: string | null;
}

/** 一份数据备份包（<dataDir>/backups/<reason>-<yyyyMMdd-HHmmss>.zip）。 */
export interface BackupSummary {
  name: string;
  path: string;
  size: number;
  createdAt: string;
  /** 备份来源：manual / pre-restore / pre-update-<version> / unknown */
  reason: string;
  appVersion: string | null;
  schemaVersion: number | null;
}

export interface BackupListResult {
  directory: string;
  dataDir: string;
  backups: BackupSummary[];
}

export interface CreateBackupResult {
  name: string;
  path: string;
  size: number;
  createdAt: string;
  backups: BackupSummary[];
}

export interface RestoreBackupResult {
  restoredFrom: string;
  preRestoreBackupPath: string;
  createdAt: string;
}

export interface OpenDataDirResult {
  path: string;
  /** 桌面壳里才会是 true；纯 WebUI 只能把路径显示给用户。 */
  opened: boolean;
}

