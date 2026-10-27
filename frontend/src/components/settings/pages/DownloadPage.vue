<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

import type { AppSettings } from "@shared/index";

import {
  clearPlaybackCache,
  connectCloud,
  disconnectCloud,
  getCloudStatus,
  getDriveNetwork,
  scanDriveNetwork,
  setDriveNetwork
} from "../../../api";
import type { DriveNetworkStatus } from "../../../api";
import SegmentedControl from "../../common/SegmentedControl.vue";
import CloudSetup from "../CloudSetup.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { openExternalLink } from "../../../lib/actions";
import { formatBytes } from "../../../lib/format";
import { isBusy, withBusy } from "../../../stores/busy";
import { saveSettingsFields, settingsForm } from "../../../stores/session";
import { toastError, updateFeedback } from "../../../stores/toasts";

const DOWNLOAD_CLIENT_OPTIONS = [
  { value: "native", label: "托管 aria2" },
  { value: "qbittorrent", label: "qBittorrent" }
] as const satisfies ReadonlyArray<{ value: AppSettings["downloadClient"]; label: string }>;

const downloadClient = computed({
  get: () => settingsForm.downloadClient,
  set: (client: AppSettings["downloadClient"]) => {
    settingsForm.downloadClient = client;
    settingsForm.qbittorrentEnabled = client === "qbittorrent";
  }
});

// —— 公共 Tracker ——
// 种子自带的 Tracker 往往不多（蜜柑的磁力尤其），多挂几个公共 Tracker 能更快找到做种者。
// 文本框里一行一个；失焦时才解析写回，免得打字到一半的地址被当成非法项丢掉。
const RECOMMENDED_TRACKERS = [
  "udp://tracker.opentrackr.org:1337/announce",
  "udp://open.stealth.si:80/announce",
  "udp://tracker.torrent.eu.org:451/announce",
  "udp://exodus.desync.com:6969/announce",
  "http://nyaa.tracker.wf:7777/announce"
];
const TRACKER_SCHEMES = ["udp:", "http:", "https:", "wss:"];

const trackerDraft = ref("");
const invalidTrackers = ref<string[]>([]);

watch(
  () => settingsForm.btTrackers,
  (trackers) => {
    trackerDraft.value = (trackers ?? []).join("\n");
  },
  { immediate: true }
);

function isValidTracker(value: string): boolean {
  try {
    const url = new URL(value);
    return TRACKER_SCHEMES.includes(url.protocol) && Boolean(url.host);
  } catch {
    return false;
  }
}

function commitTrackers(): void {
  const lines = [...new Set(trackerDraft.value.split(/[\s,]+/u).map((line) => line.trim()).filter(Boolean))];
  invalidTrackers.value = lines.filter((line) => !isValidTracker(line));
  // 非法项留在文本框里给用户改，只把合法的写进设置
  const valid = lines.filter(isValidTracker);
  if (valid.join("\n") !== (settingsForm.btTrackers ?? []).join("\n")) {
    settingsForm.btTrackers = valid;
  }
  if (invalidTrackers.value.length) {
    trackerDraft.value = [...valid, ...invalidTrackers.value].join("\n");
  }
}

function fillRecommendedTrackers(): void {
  const merged = [...new Set([...(settingsForm.btTrackers ?? []), ...RECOMMENDED_TRACKERS])];
  settingsForm.btTrackers = merged;
  invalidTrackers.value = [];
  void saveSettingsFields(["btTrackers"]);
}

// —— Google Drive 授权 ——
const cloudSetup = ref<InstanceType<typeof CloudSetup> | null>(null);
const cloudConnected = ref(false);

async function refreshCloud(): Promise<void> {
  try {
    cloudConnected.value = (await getCloudStatus()).connected;
  } catch {
    // 未登录时由登录页处理
  }
}

async function authorizeCloud(): Promise<void> {
  try {
    if (cloudConnected.value) {
      await disconnectCloud();
      settingsForm.cloudArchiveEnabled = false;
      await refreshCloud();
    } else {
      await saveSettingsFields(["googleClientId", "googleClientSecret", "googleRedirectUri"]);
      const result = await connectCloud();
      await openExternalLink(result.url);
    }
  } catch (error) {
    toastError(error, "云盘连接失败");
  }
}

// —— Google Drive 网络直连 ——
const driveNetwork = ref<DriveNetworkStatus | null>(null);
const driveCandidates = ref("");
const driveSNIMode = ref<DriveNetworkStatus["sniMode"]>("original");
const driveSNIHost = ref("");
const driveScanning = ref(false);
const driveNetworkSaving = ref(false);
const driveNetworkLocked = computed(() => !driveNetwork.value || driveScanning.value || driveNetworkSaving.value);

function applyDriveNetwork(value: DriveNetworkStatus): void {
  driveNetwork.value = value;
  driveSNIMode.value = value.sniMode;
  driveSNIHost.value = value.sniHost;
}

async function refreshDriveNetwork(): Promise<void> {
  try {
    applyDriveNetwork(await getDriveNetwork());
  } catch (error) {
    toastError(error, "读取云盘网络状态失败");
  }
}

async function saveDriveTLS(): Promise<void> {
  driveNetworkSaving.value = true;
  try {
    applyDriveNetwork(await setDriveNetwork({ sniMode: driveSNIMode.value, sniHost: driveSNIHost.value }));
  } catch (error) {
    toastError(error, "保存 SNI 设置失败");
  } finally {
    driveNetworkSaving.value = false;
  }
}

async function toggleDriveNetwork(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement;
  driveNetworkSaving.value = true;
  try {
    driveNetwork.value = await setDriveNetwork({ enabled: input.checked });
  } catch (error) {
    input.checked = driveNetwork.value?.enabled ?? false;
    toastError(error, "保存云盘网络设置失败");
  } finally {
    driveNetworkSaving.value = false;
  }
}

async function scanCloudNetwork(): Promise<void> {
  driveScanning.value = true;
  try {
    applyDriveNetwork(await setDriveNetwork({ sniMode: driveSNIMode.value, sniHost: driveSNIHost.value }));
    driveNetwork.value = await scanDriveNetwork(driveCandidates.value);
  } catch (error) {
    toastError(error, "云盘 IP 扫描失败");
  } finally {
    driveScanning.value = false;
  }
}

function driveResultLabel(result: DriveNetworkStatus["results"][number]): string {
  if (!result.available) {
    return result.error ?? "";
  }
  if (Date.now() / 1000 - result.verifiedAt >= 86400) {
    return "已过期，请重新扫描";
  }
  return result.warning || "API 与下载探测通过";
}

// —— 在线播放 ——
const clearingPlaybackCache = computed(() => isBusy("playback-cache:clear"));

async function runClearPlaybackCache(): Promise<void> {
  await withBusy("playback-cache:clear", async () => {
    try {
      const result = await clearPlaybackCache();
      updateFeedback(
        result.removedCount
          ? `已清理 ${result.removedCount} 份播放缓存，释放 ${formatBytes(result.freedBytes)}。`
          : "没有需要清理的播放缓存。",
        "success"
      );
    } catch (error) {
      toastError(error, "清理播放缓存失败。");
    }
  });
}

onMounted(() => {
  void refreshDriveNetwork();
  void refreshCloud();
  window.addEventListener("focus", refreshCloud);
});

onUnmounted(() => window.removeEventListener("focus", refreshCloud));
</script>

<template>
  <SettingsSection id="download" title="目录与下载器">
    <div class="settings-group">
      <SettingsItem stacked title="下载目录" desc="新任务默认存到这里">
        <input v-model="settingsForm.downloadDir" class="settings-input settings-input-wide" />
      </SettingsItem>

      <CloudSetup ref="cloudSetup" @connected="refreshCloud" />

      <SettingsItem title="自动归档到 Google Drive" desc="完整下载后上传全部选中文件；失败保留本地文件，每 5 分钟重试">
        <input v-model="settingsForm.cloudArchiveEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <SettingsItem
        as="div"
        title="Google Drive 授权"
        :desc="cloudConnected ? '已连接，凭据仅保存在服务端' : '尚未连接，请使用上方配置向导'"
      >
        <button type="button" class="settings-secondary-button" @click="cloudConnected ? authorizeCloud() : cloudSetup?.start()">
          {{ cloudConnected ? "断开连接" : "连接 Google Drive" }}
        </button>
      </SettingsItem>

      <details>
        <summary class="settings-title">高级设置：网络直连、TLS 与手动配置</summary>

        <div class="settings-group mt-3">
          <SettingsItem
            title="Google Drive IP 直连"
            desc="开启后，上传及云端播放使用最近 24 小时内探测最快的可用 IP；无可用地址时走系统网络。OAuth 授权仍走系统网络。"
          >
            <input
              type="checkbox"
              class="settings-switch"
              :checked="driveNetwork?.enabled ?? false"
              :disabled="!driveNetwork || driveNetworkSaving"
              @change="toggleDriveNetwork"
            />
          </SettingsItem>

          <SettingsItem as="div" stacked title="TLS SNI 模式">
            <template #desc>
              仅修改握手发送的 SNI，证书链、有效期和真实 Google 域名仍会校验。扫描、上传及云端播放使用相同模式；OAuth 使用系统网络。不发送或自定义 SNI 不保证所有网络都可直连。
            </template>
            <div class="grid w-full min-w-0 gap-2">
              <select v-model="driveSNIMode" aria-label="TLS SNI 模式" class="settings-input settings-input-wide" :disabled="driveScanning || driveNetworkSaving">
                <option value="original">原始 Google 域名（默认）</option>
                <option value="none">不发送 SNI</option>
                <option value="custom">自定义 SNI 域名</option>
              </select>
              <input
                v-if="driveSNIMode === 'custom'"
                v-model="driveSNIHost"
                aria-label="自定义 SNI 域名"
                class="settings-input settings-input-wide"
                placeholder="仅域名，不含 https://、端口或路径"
                :disabled="driveScanning || driveNetworkSaving"
              />
              <div class="settings-actions">
                <button type="button" class="settings-secondary-button" :disabled="driveNetworkLocked" @click="saveDriveTLS">保存 SNI 设置</button>
                <span class="settings-desc">修改模式会清除旧扫描结果；扫描按钮会先保存当前模式。</span>
              </div>
            </div>
          </SettingsItem>

          <SettingsItem as="div" stacked title="自定义候选 IP（可选）">
            <template #desc>
              扫描在运行 bgmcat 的机器上进行。这里测量响应时间，不代表实际下载带宽；不会生成浏览器直链。TUN 代理仍可能接管网络。
            </template>
            <div class="grid w-full min-w-0 gap-2">
              <textarea
                v-model="driveCandidates"
                aria-label="自定义候选 IP"
                class="settings-input settings-input-wide"
                rows="3"
                placeholder="每行一个公网 IP，支持 # 注释。留空使用 DNS、历史记录和内置候选。最多 256 个。"
              />
              <div class="settings-actions break-all">
                <button type="button" class="settings-secondary-button" :disabled="driveNetworkLocked" @click="scanCloudNetwork">
                  {{ driveScanning ? "正在探测，最长约 60 秒…" : "扫描可用 IP" }}
                </button>
                <span class="settings-desc">
                  {{
                    driveNetwork?.enabled
                      ? driveNetwork.selectedIP
                        ? `当前选择：${driveNetwork.selectedIP}`
                        : "当前使用系统网络：没有有效候选"
                      : "直连未启用，当前使用系统网络"
                  }}
                </span>
              </div>
              <p v-if="driveNetwork?.scannedAt" class="settings-desc">
                最近扫描：{{ new Date(driveNetwork.scannedAt).toLocaleString() }}{{ driveNetwork.partial ? "（达到时间上限，部分候选未完成）" : "" }}
              </p>
              <div v-if="driveNetwork?.results.length" class="max-h-56 overflow-auto">
                <table class="w-full text-left text-xs">
                  <thead>
                    <tr class="text-[var(--muted)]"><th class="p-2">IP</th><th class="p-2">探测耗时</th><th class="p-2">状态</th></tr>
                  </thead>
                  <tbody>
                    <tr v-for="result in driveNetwork.results" :key="result.ip" class="border-t border-[var(--line)]">
                      <td class="p-2 font-mono">{{ result.ip }}</td>
                      <td class="p-2">{{ result.available ? `${result.latencyMs} ms` : "—" }}</td>
                      <td class="p-2">{{ driveResultLabel(result) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </SettingsItem>

          <SettingsItem stacked title="Google OAuth 客户端 ID">
            <input v-model="settingsForm.googleClientId" class="settings-input settings-input-wide" placeholder="例如 xxx.apps.googleusercontent.com" />
          </SettingsItem>
          <SettingsItem stacked title="Google OAuth 客户端密钥" desc="在 Google Cloud 创建 Web 应用 OAuth 客户端，并启用 Drive API">
            <input
              v-model="settingsForm.googleClientSecret"
              class="settings-input settings-input-wide"
              type="password"
              autocomplete="off"
              placeholder="OAuth Client Secret"
            />
          </SettingsItem>
          <SettingsItem stacked title="Google Drive 目标文件夹 ID" desc="填写文件夹链接中 folders/ 后的 ID，root 表示云盘根目录">
            <input v-model="settingsForm.cloudDriveFolderId" class="settings-input settings-input-wide" placeholder="root" />
          </SettingsItem>
          <SettingsItem
            stacked
            title="OAuth 回调地址"
            desc="将这个地址添加到 Google OAuth 客户端的授权重定向 URI；播放由 Go 按需读取云端数据"
          >
            <input v-model="settingsForm.googleRedirectUri" class="settings-input settings-input-wide" placeholder="http://127.0.0.1:3001/api/cloud/callback" />
          </SettingsItem>
          <SettingsItem
            title="归档后清理本地文件"
            desc="仅在云端大小与 MD5 校验通过、云端可读取且文件没有正在播放后删除；未完成配置时保留本地文件"
          >
            <input v-model="settingsForm.cloudDeleteLocal" type="checkbox" class="settings-switch" />
          </SettingsItem>
        </div>
      </details>

      <SettingsItem as="div" title="下载器" desc="Go 托管 aria2 HTTP / BitTorrent 下载器，也可连接 qBittorrent">
        <SegmentedControl v-model="downloadClient" :options="DOWNLOAD_CLIENT_OPTIONS" aria-label="下载器" />
      </SettingsItem>

      <SettingsItem
        v-if="settingsForm.downloadClient === 'native'"
        as="div"
        stacked
        title="公共 Tracker"
        desc="下载磁力或种子时额外挂上这些 Tracker（与种子自带的并用），做种者少的番剧更容易连上。一行一个，留空则只用种子自带的。改动对之后新加的任务生效。"
      >
        <div class="grid w-full min-w-0 gap-2">
          <textarea
            v-model="trackerDraft"
            aria-label="公共 Tracker 列表"
            class="settings-input settings-input-wide font-mono text-xs"
            rows="4"
            spellcheck="false"
            placeholder="udp://tracker.opentrackr.org:1337/announce"
            @blur="commitTrackers"
            @keydown.enter.stop
          />
          <div class="settings-actions">
            <button type="button" class="settings-secondary-button" @click="fillRecommendedTrackers">填入常用列表</button>
            <span class="settings-desc">{{ settingsForm.btTrackers.length ? `已启用 ${settingsForm.btTrackers.length} 个` : "未添加，只用种子自带的 Tracker" }}</span>
          </div>
          <p v-if="invalidTrackers.length" class="settings-field-error">
            这些地址无效，未保存：{{ invalidTrackers.join("、") }}（需以 udp://、http://、https:// 或 wss:// 开头）
          </p>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection v-if="settingsForm.downloadClient === 'qbittorrent'" id="qbittorrent" title="qBittorrent">
    <div class="settings-group">
      <!-- 选下载器时会一并打开它；只有旧配置里「选了 qBittorrent 却关着连接」才需要这一行 -->
      <SettingsItem v-if="!settingsForm.qbittorrentEnabled" title="连接已停用" desc="当前不会向 qBittorrent 发送任何任务，打开后才会生效">
        <input v-model="settingsForm.qbittorrentEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <SettingsItem stacked title="Web UI 地址" desc="qBittorrent 设置里「Web UI」那一页的地址，例如 http://127.0.0.1:8080">
        <input v-model="settingsForm.qbittorrentBaseUrl" class="settings-input settings-input-wide" placeholder="http://127.0.0.1:8080" />
      </SettingsItem>

      <SettingsItem title="登录账号" desc="Web UI 的用户名与密码">
        <span class="settings-pair">
          <input v-model="settingsForm.qbittorrentUsername" class="settings-input" autocomplete="off" placeholder="用户名" aria-label="qBittorrent 用户名" />
          <input
            v-model="settingsForm.qbittorrentPassword"
            type="text"
            class="secret-mask-input settings-input"
            autocomplete="off"
            placeholder="密码"
            aria-label="qBittorrent 密码"
          />
        </span>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="playback" title="在线播放">
    <div class="settings-group">
      <SettingsItem as="div" title="播放缓存" desc="在线播放 MKV / HEVC 时产生的转码分片；清理会中断正在进行的转码播放">
        <button type="button" class="settings-secondary-button" :disabled="clearingPlaybackCache" @click="runClearPlaybackCache">
          {{ clearingPlaybackCache ? "清理中…" : "清理播放缓存" }}
        </button>
      </SettingsItem>
    </div>
    <details class="settings-advanced">
      <summary>高级</summary>
      <div class="settings-group">
        <SettingsItem
          stacked
          title="ffmpeg 路径"
          desc="留空使用随应用打包的 ffmpeg；填写可执行文件路径可覆盖，ffprobe 取同目录的同名文件"
        >
          <input v-model="settingsForm.ffmpegPath" class="settings-input settings-input-wide" placeholder="留空使用内置 ffmpeg，例如 /opt/homebrew/bin/ffmpeg" />
        </SettingsItem>
      </div>
    </details>
  </SettingsSection>
</template>
