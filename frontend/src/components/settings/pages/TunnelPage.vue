<script setup lang="ts">
import { computed, onMounted } from "vue";

import SegmentedControl from "../../common/SegmentedControl.vue";
import MobileAccessQrCard from "../MobileAccessQrCard.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { settingsForm } from "../../../stores/session";
import {
  addTunnelOriginToCors,
  applyDiscoveredTunnel,
  discoveredTunnels,
  refreshDiscoveredTunnels,
  refreshTunnel,
  refreshTunnelLogs,
  restartTunnelAction,
  setTunnelEnabled,
  tunnelBlockers,
  tunnelBusy,
  tunnelLogs,
  tunnelStatus,
  tunnelWarnings
} from "../../../stores/tunnel";

const TUNNEL_MODE_OPTIONS = [
  { value: "quick", label: "临时隧道" },
  { value: "named", label: "命名隧道" }
] as const;

const CREDENTIAL_MODE_OPTIONS = [
  { value: "credentials-file", label: "凭据文件" },
  { value: "token", label: "Token" }
] as const;

const BINARY_SOURCE_LABELS: Record<string, string> = {
  override: "自定义路径",
  bundled: "内置",
  path: "系统 PATH"
};

const tunnelBinaryLabel = computed(() => {
  const snapshot = tunnelStatus.value;
  if (!snapshot?.binaryAvailable) {
    return "未找到";
  }
  return BINARY_SOURCE_LABELS[snapshot.binarySource ?? "path"] ?? "可用";
});

onMounted(() => {
  // 隧道明细只在打开设置页时拉一次：实时状态已经由 boot.ts 的 status 轮询带回来了
  void refreshTunnel();
  void refreshTunnelLogs();
  void refreshDiscoveredTunnels();
});
</script>

<template>
  <SettingsSection id="tunnel" title="Cloudflare 隧道">
    <div v-if="tunnelBlockers.length || tunnelWarnings.length" class="tunnel-checks">
      <p
        v-for="issue in [...tunnelBlockers, ...tunnelWarnings]"
        :key="issue.code"
        class="tunnel-check"
        :class="issue.level === 'block' ? 'tunnel-check-block' : 'tunnel-check-warn'"
      >
        <strong>{{ issue.level === "block" ? "阻断" : "提醒" }}</strong>
        <span>{{ issue.message }}</span>
        <button v-if="issue.code === 'corsRestricted'" type="button" class="tunnel-check-fix" @click="addTunnelOriginToCors()">加入允许来源</button>
      </p>
    </div>

    <MobileAccessQrCard />

    <div class="settings-group">
      <SettingsItem title="启用隧道">
        <input
          :checked="settingsForm.tunnelEnabled"
          :disabled="tunnelBusy || (!settingsForm.tunnelEnabled && tunnelBlockers.length > 0)"
          type="checkbox"
          class="settings-switch"
          @change="setTunnelEnabled(($event.target as HTMLInputElement).checked)"
        />
      </SettingsItem>

      <SettingsItem as="div" title="隧道模式">
        <SegmentedControl v-model="settingsForm.tunnelMode" :options="TUNNEL_MODE_OPTIONS" aria-label="隧道模式" />
      </SettingsItem>

      <template v-if="settingsForm.tunnelMode === 'named'">
        <SettingsItem stacked title="公网主机名">
          <input v-model="settingsForm.tunnelHostname" class="settings-input settings-input-wide" placeholder="anime.example.com" />
        </SettingsItem>

        <SettingsItem as="div" title="凭据方式">
          <SegmentedControl v-model="settingsForm.tunnelCredentialMode" :options="CREDENTIAL_MODE_OPTIONS" aria-label="凭据方式" />
        </SettingsItem>

        <template v-if="settingsForm.tunnelCredentialMode === 'credentials-file'">
          <SettingsItem stacked title="隧道 UUID">
            <input v-model="settingsForm.tunnelId" class="settings-input settings-input-wide" />
          </SettingsItem>

          <SettingsItem stacked title="凭据文件">
            <input v-model="settingsForm.tunnelCredentialsFile" class="settings-input settings-input-wide" placeholder="~/.cloudflared/<uuid>.json" />
          </SettingsItem>

          <SettingsItem v-if="discoveredTunnels.length" as="div" stacked title="本机已有隧道">
            <div class="settings-actions">
              <button
                v-for="item in discoveredTunnels"
                :key="item.uuid"
                type="button"
                class="settings-secondary-button"
                @click="applyDiscoveredTunnel(item)"
              >
                {{ item.hostname ?? item.uuid }}
              </button>
            </div>
          </SettingsItem>
        </template>

        <SettingsItem v-else stacked title="隧道 Token">
          <input v-model="settingsForm.tunnelToken" type="text" class="secret-mask-input settings-input settings-input-wide" autocomplete="off" spellcheck="false" />
        </SettingsItem>
      </template>

      <SettingsItem title="崩溃自动重启">
        <input v-model="settingsForm.tunnelAutoRestart" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <SettingsItem as="div" title="重启隧道">
        <button type="button" class="settings-secondary-button" :disabled="tunnelBusy || !settingsForm.tunnelEnabled" @click="restartTunnelAction()">
          {{ tunnelBusy ? "处理中…" : "重启" }}
        </button>
      </SettingsItem>

      <SettingsItem stacked title="cloudflared 路径" :desc="`当前：${tunnelBinaryLabel}`">
        <input v-model="settingsForm.tunnelBinaryPath" class="settings-input settings-input-wide" placeholder="自动" />
      </SettingsItem>
    </div>

    <div class="tunnel-logs">
      <div class="flex items-center justify-between gap-2">
        <strong class="text-sm font-semibold text-[var(--ink)]">隧道日志（{{ tunnelLogs.length }}）</strong>
        <button type="button" class="settings-secondary-button" @click="refreshTunnelLogs()">刷新</button>
      </div>
      <ol class="tunnel-log-list">
        <li v-for="entry in tunnelLogs" :key="entry.id" :class="`tunnel-log-${entry.level}`">
          <code>{{ entry.createdAt.slice(11, 19) }}</code>
          <span>{{ entry.message }}</span>
        </li>
        <li v-if="!tunnelLogs.length">暂无日志</li>
      </ol>
    </div>
  </SettingsSection>
</template>
