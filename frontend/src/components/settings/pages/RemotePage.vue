<script setup lang="ts">
import { computed } from "vue";

import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { copyText } from "../../../lib/actions";
import { rotateToken, rotatingToken, saveSettingsFields, settings, settingsFieldErrors, settingsForm, status } from "../../../stores/session";
import { tunnelStateLabel, tunnelStatus } from "../../../stores/tunnel";

const liveTunnelState = computed(() => status.value?.tunnelState ?? tunnelStatus.value?.state);
const tunnelPublicUrl = computed(() => status.value?.tunnelPublicUrl ?? tunnelStatus.value?.publicUrl ?? "");

/**
 * 这个字段是不是被环境变量接管了（容器部署常见）。
 * 服务端在 lockedSettings 里给出字段名；禁用输入框并说明原因，
 * 比让用户改完保存、再看着它弹回去要好。
 */
function isSettingLocked(key: string): boolean {
  return (settings.value?.lockedSettings ?? []).includes(key);
}

const externalAccessEnabled = computed({
  get: () => !["127.0.0.1", "localhost"].includes(settingsForm.bindHost.trim().toLowerCase()),
  set: (enabled: boolean) => {
    // bindHost 归在延迟提交那组（它同时也是个文本框），但这里是开关，得立刻存
    settingsForm.bindHost = enabled ? "0.0.0.0" : "127.0.0.1";
    void saveSettingsFields(["bindHost"]);
  }
});

function parseList(event: Event): string[] {
  return (event.target as HTMLInputElement).value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}
</script>

<template>
  <SettingsSection id="remote-status" title="访问状态">
    <div class="settings-summary-grid">
      <button type="button" class="settings-summary-item text-left" @click="copyText(status?.apiBaseUrl ?? '')">
        <span class="settings-summary-label">本机地址（点击复制）</span>
        <strong class="break-all">{{ status?.apiBaseUrl ?? "-" }}</strong>
      </button>
      <div class="settings-summary-item">
        <span class="settings-summary-label">本机鉴权</span>
        <strong>{{ status?.localAuthBypassEnabled ? "本机免 token" : "需要 token" }}</strong>
      </div>
      <div class="settings-summary-item">
        <span class="settings-summary-label">隧道</span>
        <strong>
          {{ tunnelStateLabel(liveTunnelState) }}
          <template v-if="tunnelStatus?.readyConnections"> · {{ tunnelStatus.readyConnections }} 条连接</template>
        </strong>
      </div>
      <button type="button" class="settings-summary-item text-left" :disabled="!tunnelPublicUrl" @click="copyText(tunnelPublicUrl)">
        <span class="settings-summary-label">公网地址（点击复制）</span>
        <strong class="break-all">{{ tunnelPublicUrl || "-" }}</strong>
      </button>
    </div>
  </SettingsSection>

  <SettingsSection id="token" title="访问口令">
    <div class="settings-group">
      <SettingsItem as="div" stacked title="访问口令（Token）" desc="从其他设备打开 WebUI 时要填的口令">
        <div class="flex w-full min-w-0 flex-col gap-2 sm:flex-row">
          <input
            v-model="settingsForm.token"
            type="text"
            aria-label="访问口令"
            class="secret-mask-input settings-input min-w-0 flex-1"
            autocomplete="off"
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
            inputmode="text"
          />
          <button type="button" class="settings-secondary-button" :disabled="!settingsForm.token.trim()" @click="copyText(settingsForm.token)">复制 Token</button>
          <button type="button" class="settings-secondary-button" :disabled="rotatingToken" @click="rotateToken()">重新生成</button>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="lan" title="局域网访问">
    <div class="settings-group">
      <SettingsItem title="允许局域网 / 外网访问" desc="监听地址由启动参数控制。例如 --listen 0.0.0.0:3001 允许局域网访问。">
        <input v-model="externalAccessEnabled" :disabled="isSettingLocked('bindHost')" type="checkbox" class="settings-switch" />
      </SettingsItem>
    </div>
    <details class="settings-advanced">
      <summary>高级</summary>
      <div class="settings-group">
        <SettingsItem title="监听地址与端口" desc="WebUI/API 的 Bind Host 和 Port">
          <div class="settings-field">
            <span class="settings-field-row">
              <input
                v-model="settingsForm.bindHost"
                class="settings-input"
                :class="{ 'settings-input-invalid': settingsFieldErrors.bindHost }"
                aria-label="Bind Host"
                :disabled="isSettingLocked('bindHost')"
              />
              <input
                v-model.number="settingsForm.port"
                type="number"
                class="settings-input settings-input-short"
                :class="{ 'settings-input-invalid': settingsFieldErrors.port }"
                aria-label="Port"
                :disabled="isSettingLocked('port')"
              />
            </span>
            <!-- 容器部署里这两项由环境变量定死。不说明的话,用户改完保存、页面回弹,会以为是 bug -->
            <span v-if="isSettingLocked('bindHost') || isSettingLocked('port')" class="settings-desc">
              由环境变量接管（ANIME_RSS_BIND_HOST / ANIME_RSS_PORT），要改请改部署配置后重启容器。
            </span>
            <span v-if="settingsFieldErrors.bindHost || settingsFieldErrors.port" class="settings-field-error">
              {{ settingsFieldErrors.bindHost ?? settingsFieldErrors.port }}
            </span>
          </div>
        </SettingsItem>

        <SettingsItem stacked title="允许的跨域来源（CORS）" desc="多个来源用英文逗号分隔；开隧道时上面的检查会提示要不要加">
          <input :value="settingsForm.corsOrigins.join(', ')" class="settings-input settings-input-wide" @input="settingsForm.corsOrigins = parseList($event)" />
        </SettingsItem>
      </div>
    </details>
  </SettingsSection>

  <SettingsSection id="mcp" title="MCP">
    <div class="settings-group">
      <SettingsItem title="公网开放 MCP" desc="关闭时只有本机能连 MCP；经隧道或公网来的 /mcp 请求一律返回 404。">
        <input v-model="settingsForm.exposeMcpPublicly" type="checkbox" class="settings-switch" />
      </SettingsItem>
    </div>
  </SettingsSection>
</template>
