<script setup lang="ts">
import { computed, ref, watch } from "vue";

import type { AppSettings } from "@shared/index";

import { clearPlaybackCache } from "../../../api";
import SegmentedControl from "../../common/SegmentedControl.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
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

</script>

<template>
  <SettingsSection id="download" title="目录与下载器">
    <div class="settings-group">
      <SettingsItem stacked title="下载目录" desc="新任务默认存到这里">
        <input v-model="settingsForm.downloadDir" class="settings-input settings-input-wide" />
      </SettingsItem>

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
