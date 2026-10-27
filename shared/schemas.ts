import { z } from "zod";

export const homepageAnimeSchema = z.object({
  animeId: z.string().trim().min(1),
  bangumiId: z.string().trim().min(1).nullable(),
  title: z.string().trim().min(1),
  imageUrl: z.string().trim().url().nullable(),
  searchKeyword: z.string().trim().min(1).nullable(),
  airDay: z.number().int().min(0).max(6).nullable(),
  isOnAir: z.boolean(),
  isRestricted: z.boolean(),
  rating: z.number().nullable(),
  source: z.literal("bangumi"),
  mikanId: z.string().trim().min(1).nullable(),
  officialSite: z.string().trim().url().nullable()
});

export const subscriptionRuleSchema = z.object({
  includeKeywords: z.array(z.string().trim()).default([]),
  excludeKeywords: z.array(z.string().trim()).default([]),
  preferredResolutions: z.array(z.string().trim()).default([]),
  autoDownload: z.boolean().default(true),
  skipBatch: z.boolean().default(true),
  preferredSubgroups: z.array(z.string().trim()).default([])
});

export const createSubscriptionSchema = z.object({
  name: z.string().trim().min(1).max(120),
  url: z.string().trim().url(),
  enabled: z.boolean().default(true),
  downloadDirOverride: z.string().trim().min(1).nullable().optional(),
  rule: subscriptionRuleSchema.default({
    includeKeywords: [],
    excludeKeywords: [],
    preferredResolutions: [],
    autoDownload: true,
    skipBatch: true,
    preferredSubgroups: []
  }),
  animeId: z.string().trim().min(1).nullable().optional(),
  providerId: z.string().trim().min(1).nullable().optional()
});

export const updateSubscriptionSchema = createSubscriptionSchema.partial().extend({
  id: z.number().int().positive().optional()
});

export const updateSettingsSchema = z.object({
  bindHost: z.string().trim().min(1).optional(),
  port: z.number().int().min(1).max(65535).optional(),
  token: z.string().trim().min(24).optional(),
  corsOrigins: z.array(z.string().trim()).optional(),
  pollIntervalMinutes: z.number().int().min(1).max(1440).optional(),
  downloadDir: z.string().trim().min(1).optional(),
  cloudArchiveEnabled: z.boolean().optional(),
  dianaMcpConfigPath: z.string().trim().optional(),
  googleClientId: z.string().trim().optional(),
  googleClientSecret: z.string().trim().optional(),
  cloudDriveFolderId: z.string().trim().regex(/^[a-zA-Z0-9_-]*$/).optional(),
  googleRedirectUri: z.string().trim().optional(),
  cloudDeleteLocal: z.boolean().optional(),
  downloadClient: z.enum(["native", "qbittorrent"]).optional(),
  aria2RpcHost: z.string().trim().min(1).optional(),
  aria2RpcPort: z.number().int().min(1).max(65535).optional(),
  aria2RpcSecret: z.string().trim().min(1).optional(),
  aria2SessionPath: z.string().trim().min(1).optional(),
  aria2Enabled: z.boolean().optional(),
  aria2SeedEnabled: z.boolean().optional(),
  btTrackers: z.array(z.string().trim()).optional(),
  qbittorrentBaseUrl: z.string().trim().url().optional(),
  qbittorrentUsername: z.string().trim().min(1).optional(),
  qbittorrentPassword: z.string().optional(),
  qbittorrentEnabled: z.boolean().optional(),
  mikanBaseUrl: z.string().trim().url().optional(),
  mikanDirectAccessMode: z.enum(["prefer", "off"]).optional(),
  mikanDirectAccessEnabled: z.boolean().optional(),
  mikanResolverIps: z.array(z.string().trim()).optional(),
  localAiEnabled: z.boolean().optional(),
  localAiBaseUrl: z.string().trim().url().optional(),
  localAiModel: z.string().trim().min(1).optional(),
  localAiSystemPrompt: z.string().trim().optional(),
  homepageSortMode: z.enum(["default", "rating"]).optional(),
  /** @deprecated 已废弃，前端不再读；保留只为兼容旧配置。 */
  multiSidebarEnabled: z.boolean().optional(),
  tunnelEnabled: z.boolean().optional(),
  tunnelMode: z.enum(["quick", "named"]).optional(),
  tunnelBinaryPath: z.string().trim().optional(),
  tunnelAutoRestart: z.boolean().optional(),
  tunnelHostname: z.string().trim().optional(),
  tunnelCredentialMode: z.enum(["credentials-file", "token"]).optional(),
  tunnelCredentialsFile: z.string().trim().optional(),
  tunnelId: z.string().trim().optional(),
  tunnelToken: z.string().trim().optional(),
  exposeMcpPublicly: z.boolean().optional(),
  githubOAuthEnabled: z.boolean().optional(),
  githubClientId: z.string().trim().optional(),
  githubClientSecret: z.string().trim().optional(),
  githubAllowedLogins: z.array(z.string().trim()).optional(),
  ffmpegPath: z.string().trim().optional(),
  updateAutoDownload: z.boolean().optional(),
  updateAutoInstall: z.boolean().optional(),
  updateMirror: z.string().trim().optional(),
  updateChannel: z.enum(["stable"]).optional(),
  launchAtLogin: z.boolean().optional(),
  minimizeToTray: z.boolean().optional(),
  enabledResourceProviders: z.array(z.string().trim()).optional(),
  resourceProviderOrder: z.array(z.string().trim()).optional(),
  enabledMetadataPlatforms: z.array(z.string().trim()).optional(),
  tmdbApiKey: z.string().trim().optional(),
  // 主题色只收 #rrggbb：非法值直接拒绝，免得把垃圾灌进 CSS 变量
  themeAccent: z
    .string()
    .trim()
    .regex(/^#[0-9a-fA-F]{6}$/u, "主题色必须是 #rrggbb 形式的十六进制颜色")
    .optional()
});

export const githubExchangeSchema = z.object({
  code: z.string().trim().min(1).max(200)
});

export const localAiChatMessageSchema = z.object({
  role: z.enum(["user", "assistant"]),
  content: z.string().trim().min(1)
});

export const localAiChatRequestSchema = z.object({
  messages: z.array(localAiChatMessageSchema).min(1).max(24)
});

export const tokenLoginSchema = z.object({
  token: z.string().trim().min(1)
});

export const createDownloadSchema = z.object({
  feedItemId: z.number().int().positive().optional(),
  url: z.string().trim().min(1).optional(),
  title: z.string().trim().min(1).max(500).optional(),
  imageUrl: z.string().trim().url().nullable().optional(),
  animeId: z.string().trim().min(1).optional(),
  pageUrl: z.string().trim().min(1).optional(),
  sourceType: z.enum(["direct", "magnet", "torrent-url"]).optional()
}).refine((value) => Boolean(value.feedItemId || value.url), {
  message: "Either feedItemId or url is required."
});

export const launchPlayerSchema = z.object({
  playerId: z.enum(["system", "vlc", "mpv", "potplayer", "mpc-hc"]).default("system")
});

export const resolveDownloadConflictSchema = z.object({
  strategy: z.enum(["overwrite", "delete-existing", "recheck"])
});

export const removeDownloadSchema = z.object({
  deleteLocalFiles: z.boolean().default(false)
});

export const createFavoriteAnimeSchema = z.object({
  anime: homepageAnimeSchema,
  mikanKeyword: z.string().trim().min(1).optional(),
  /** 要挂哪些源。省略时用设置里启用的源。 */
  providerIds: z.array(z.string().trim().min(1)).optional()
});
