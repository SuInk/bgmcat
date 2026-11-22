<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { Eye, EyeOff, KeyRound, LogOut, RefreshCw } from "@lucide/vue";

import AppDialog from "../../common/AppDialog.vue";
import MobileAccessQrCard from "../MobileAccessQrCard.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { copyText } from "../../../lib/actions";
import { isAndroidAppRuntime, openAndroidServerSwitcher, readAndroidServerLabel } from "../../../api";
import type { AuthSessionSummary } from "@shared/index";
import {
  adminUsername,
  authSessions,
  cloudDriveConfigured,
  dianaAuthAvailable,
  dianaOwnerPairAvailable,
  loadingSessions,
  otherSessionCount,
  passwordConfigured,
  refreshAuthSessions,
  refreshAuthStatus,
  revokeOtherSessions,
  revokeSession,
  rotateToken,
  rotatingToken,
  saveAdminCredentials,
  savingCredentials,
  saveSettingsFields,
  settings,
  settingsFieldErrors,
  settingsForm,
  status
} from "../../../stores/session";
import { tunnelStateLabel, tunnelStatus } from "../../../stores/tunnel";

const confirmRotateTokenOpen = ref(false);
const pendingRevokeSession = ref<AuthSessionSummary | null>(null);
const confirmRevokeOthersOpen = ref(false);
const revokingSession = ref(false);

async function confirmRotateToken(): Promise<void> {
  await rotateToken();
  confirmRotateTokenOpen.value = false;
}

async function confirmRevokeSingleSession(): Promise<void> {
  const target = pendingRevokeSession.value;
  if (!target) return;
  revokingSession.value = true;
  try {
    await revokeSession(target.id);
    pendingRevokeSession.value = null;
  } finally {
    revokingSession.value = false;
  }
}

async function confirmRevokeOthers(): Promise<void> {
  revokingSession.value = true;
  try {
    await revokeOtherSessions();
    confirmRevokeOthersOpen.value = false;
  } finally {
    revokingSession.value = false;
  }
}

const liveTunnelState = computed(() => status.value?.tunnelState ?? tunnelStatus.value?.state);
const tunnelPublicUrl = computed(() => status.value?.tunnelPublicUrl ?? tunnelStatus.value?.publicUrl ?? "");

const usernameDraft = ref(adminUsername.value);
const newPassword = ref("");
const showNewPassword = ref(false);
const showApiToken = ref(false);

watch(adminUsername, (next) => {
  if (next && !usernameDraft.value) {
    usernameDraft.value = next;
  }
});

const canSubmitCredentials = computed(() => {
  if (savingCredentials.value || newPassword.value.length === 0) {
    return false;
  }
  return (usernameDraft.value.trim() || adminUsername.value.trim()).length >= 2;
});

async function submitCredentials(): Promise<void> {
  if (!canSubmitCredentials.value) {
    return;
  }
  const ok = await saveAdminCredentials({
    newUsername: usernameDraft.value.trim() || adminUsername.value.trim(),
    newPassword: newPassword.value
  });
  if (ok) {
    usernameDraft.value = adminUsername.value;
    newPassword.value = "";
    showNewPassword.value = false;
  }
}

function formatSessionTime(value: string): string {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function isSettingLocked(key: string): boolean {
  return (settings.value?.lockedSettings ?? []).includes(key);
}

const isWildcardBind = computed(() => ["0.0.0.0", "::", "[::]", ""].includes(settingsForm.bindHost.trim().toLowerCase()));
const lanUrls = computed(() => status.value?.lanUrls ?? []);
const isAndroidApp = isAndroidAppRuntime();
const androidServerLabel = computed(() => (isAndroidApp ? readAndroidServerLabel() : ""));

const lanToggleModel = computed({
  get: () => isWildcardBind.value || Boolean(settingsForm.lanAccessEnabled),
  set: (enabled: boolean) => {
    if (isWildcardBind.value) {
      return;
    }
    settingsForm.lanAccessEnabled = enabled;
    void saveSettingsFields(["lanAccessEnabled"]);
  }
});

function parseList(event: Event): string[] {
  return (event.target as HTMLInputElement).value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

const SESSION_METHOD_LABELS = (): Record<string, string> => ({
  github: "GitHub",
  google: "Google",
  diana: "Diana",
  qrcode: "扫码",
  oidc: settingsForm.oidcDisplayName.trim() || "SSO"
});

function formatSessionProviderTag(session: AuthSessionSummary): string {
  const login = session.githubLogin?.trim() || "";
  const method = session.authMethod?.trim() || "";
  const label = SESSION_METHOD_LABELS()[method];
  if (label) {
    return login ? `${label} · ${login}` : label;
  }
  return login;
}

onMounted(() => {
  void refreshAuthStatus().then(() => {
    if (adminUsername.value) {
      usernameDraft.value = adminUsername.value;
    }
  });
  void refreshAuthSessions();
});
</script>

<template>
  <SettingsSection id="remote-status" title="访问状态">
    <div class="settings-summary-grid">
      <button type="button" class="settings-summary-item text-left" @click="copyText(status?.apiBaseUrl ?? '')">
        <span class="settings-summary-label">本机地址（点击复制）</span>
        <strong class="break-all">{{ status?.apiBaseUrl ?? "-" }}</strong>
      </button>
      <div class="settings-summary-item">
        <span class="settings-summary-label">管理员账号</span>
        <strong>{{ adminUsername || "-" }}</strong>
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

    <MobileAccessQrCard class="mt-3" />
  </SettingsSection>

  <SettingsSection id="security" title="账号与密码">
    <div class="settings-group" @focusout.stop @keydown.enter.stop.prevent="submitCredentials">
      <SettingsItem stacked title="管理账号">
        <input
          v-model="usernameDraft"
          class="settings-input settings-input-wide"
          type="text"
          :placeholder="adminUsername || '2–64 位字符'"
          autocomplete="username"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
        />
      </SettingsItem>

      <SettingsItem stacked :title="passwordConfigured ? '新密码' : '设置密码'">
        <div class="sec-password-wrap settings-input-wide">
          <input
            v-model="newPassword"
            class="settings-input w-full pr-10"
            :type="showNewPassword ? 'text' : 'password'"
            placeholder="至少 8 位"
            autocomplete="new-password"
          />
          <button
            type="button"
            class="sec-password-toggle"
            :aria-label="showNewPassword ? '隐藏新密码' : '显示新密码'"
            @click="showNewPassword = !showNewPassword"
          >
            <EyeOff v-if="showNewPassword" :size="15" aria-hidden="true" />
            <Eye v-else :size="15" aria-hidden="true" />
          </button>
        </div>
      </SettingsItem>

      <SettingsItem as="div" title="登录保护">
        <div class="flex items-center gap-2">
          <span v-if="passwordConfigured" class="status-pill status-pill-ok">已启用</span>
          <button
            type="button"
            class="btn-primary inline-flex items-center gap-1.5 px-3.5 py-1.5 text-xs font-semibold"
            :disabled="!canSubmitCredentials"
            @click="submitCredentials"
          >
            <KeyRound :size="14" aria-hidden="true" />
            <span>{{ savingCredentials ? "保存中…" : passwordConfigured ? "更新密码" : "保存密码" }}</span>
          </button>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="oauth" title="第三方与快捷登录">
    <div class="settings-group">
      <SettingsItem title="Diana 快捷登录">
        <template #desc>
          复用本机或远端 Diana 账号密码及机器人私聊 6 位验证码登录
          <span v-if="settingsForm.dianaAuthEnabled && dianaAuthAvailable" class="ml-1.5 text-[var(--accent)]">
            {{ dianaOwnerPairAvailable ? "（已检测到私聊验证码通道）" : "（已开启账号登录）" }}
          </span>
        </template>
        <input v-model="settingsForm.dianaAuthEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <template v-if="settingsForm.dianaAuthEnabled">
        <SettingsItem stacked title="Diana 服务地址">
          <div class="settings-field w-full">
            <input
              v-model="settingsForm.dianaAuthBaseUrl"
              class="settings-input settings-input-wide"
              :class="{ 'settings-input-invalid': settingsFieldErrors.dianaAuthBaseUrl }"
              placeholder="留空自动探测本机 Diana，或填 http://127.0.0.1:18080"
              autocomplete="off"
              spellcheck="false"
            />
            <span v-if="settingsFieldErrors.dianaAuthBaseUrl" class="settings-field-error">
              {{ settingsFieldErrors.dianaAuthBaseUrl }}
            </span>
          </div>
        </SettingsItem>
      </template>

      <SettingsItem title="Google 登录">
        <input v-model="settingsForm.googleOAuthEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <template v-if="settingsForm.googleOAuthEnabled">
        <SettingsItem stacked title="Google Client ID">
          <input
            v-model="settingsForm.googleAuthClientId"
            class="settings-input settings-input-wide"
            :placeholder="cloudDriveConfigured ? '留空将复用 Google 云盘 Client ID' : '输入 Google OAuth Client ID'"
            autocomplete="off"
            spellcheck="false"
          />
        </SettingsItem>

        <SettingsItem stacked title="Google Client Secret">
          <template #desc>回调 <code>/api/auth/google/callback</code>（留空则复用云盘密钥）</template>
          <input
            v-model="settingsForm.googleAuthClientSecret"
            class="secret-mask-input settings-input settings-input-wide"
            autocomplete="off"
            spellcheck="false"
          />
        </SettingsItem>

        <SettingsItem stacked title="Google 白名单邮箱">
          <input
            :value="settingsForm.googleAllowedEmails.join(', ')"
            class="settings-input settings-input-wide"
            placeholder="多个邮箱以逗号分隔（必填）"
            @input="settingsForm.googleAllowedEmails = parseList($event)"
          />
        </SettingsItem>
      </template>

      <SettingsItem title="GitHub 登录">
        <input v-model="settingsForm.githubOAuthEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <template v-if="settingsForm.githubOAuthEnabled">
        <SettingsItem stacked title="GitHub Client ID">
          <input v-model="settingsForm.githubClientId" class="settings-input settings-input-wide" autocomplete="off" spellcheck="false" />
        </SettingsItem>

        <SettingsItem stacked title="GitHub Client Secret">
          <template #desc>回调 <code>/api/auth/github/callback</code></template>
          <input v-model="settingsForm.githubClientSecret" class="secret-mask-input settings-input settings-input-wide" autocomplete="off" spellcheck="false" />
        </SettingsItem>

        <SettingsItem stacked title="GitHub 白名单账号">
          <input
            :value="settingsForm.githubAllowedLogins.join(', ')"
            class="settings-input settings-input-wide"
            placeholder="多个用户名以逗号分隔"
            @input="settingsForm.githubAllowedLogins = parseList($event)"
          />
        </SettingsItem>
      </template>

      <SettingsItem title="通用 OIDC / SSO 登录" desc="支持 Pocket ID、Authentik、Authelia、Keycloak、Linux.do 等 OpenID Connect 服务">
        <input v-model="settingsForm.oidcEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <template v-if="settingsForm.oidcEnabled">
        <SettingsItem stacked title="按钮显示名称">
          <input v-model="settingsForm.oidcDisplayName" class="settings-input settings-input-wide" placeholder="SSO" autocomplete="off" />
        </SettingsItem>

        <SettingsItem stacked title="Issuer URL">
          <template #desc>自动读取 <code>/.well-known/openid-configuration</code>，回调 <code>/api/auth/oidc/callback</code></template>
          <div class="settings-field w-full">
            <input
              v-model="settingsForm.oidcIssuerUrl"
              class="settings-input settings-input-wide"
              :class="{ 'settings-input-invalid': settingsFieldErrors.oidcIssuerUrl }"
              placeholder="https://auth.example.com"
              autocomplete="off"
              spellcheck="false"
            />
            <span v-if="settingsFieldErrors.oidcIssuerUrl" class="settings-field-error">
              {{ settingsFieldErrors.oidcIssuerUrl }}
            </span>
          </div>
        </SettingsItem>

        <SettingsItem stacked title="Client ID">
          <input v-model="settingsForm.oidcClientId" class="settings-input settings-input-wide" autocomplete="off" spellcheck="false" />
        </SettingsItem>

        <SettingsItem stacked title="Client Secret">
          <input
            v-model="settingsForm.oidcClientSecret"
            class="secret-mask-input settings-input settings-input-wide"
            placeholder="公开客户端可留空（支持 PKCE S256）"
            autocomplete="off"
            spellcheck="false"
          />
        </SettingsItem>

        <SettingsItem stacked title="允许登录的用户">
          <input
            :value="settingsForm.oidcAllowedUsers.join(', ')"
            class="settings-input settings-input-wide"
            placeholder="邮箱、用户名或 sub，逗号分隔；填 * 允许该 IdP 全部用户"
            @input="settingsForm.oidcAllowedUsers = parseList($event)"
          />
        </SettingsItem>
      </template>
    </div>
  </SettingsSection>

  <SettingsSection id="sessions" title="登录设备">
    <div class="settings-group">
      <SettingsItem as="div" stacked>
        <div class="grid w-full min-w-0 gap-2.5">
          <p v-if="loadingSessions && !authSessions.length" class="settings-desc">加载中…</p>
          <p v-else-if="!authSessions.length" class="settings-desc">暂无登录会话</p>
          <ul v-else class="sec-session-list">
            <li v-for="session in authSessions" :key="session.id" class="sec-session-item">
              <div class="sec-session-main">
                <span class="sec-session-name">
                  <strong>{{ session.deviceName || "未知设备" }}</strong>
                  <span v-if="formatSessionProviderTag(session)" class="sec-session-tag">{{ formatSessionProviderTag(session) }}</span>
                  <span v-if="session.current" class="status-pill status-pill-ok">当前</span>
                </span>
                <span class="sec-session-meta">
                  {{ session.ipAddress || "IP 未知" }} · {{ formatSessionTime(session.lastSeenAt) }}
                </span>
                <span v-if="session.userAgent" class="sec-session-ua">{{ session.userAgent }}</span>
              </div>
              <button
                type="button"
                class="settings-secondary-button sec-danger-btn shrink-0"
                @click="pendingRevokeSession = session"
              >
                <LogOut :size="13" aria-hidden="true" />
                <span>{{ session.current ? "退出" : "移除" }}</span>
              </button>
            </li>
          </ul>
          <div class="flex flex-wrap items-center gap-2 pt-1">
            <button type="button" class="settings-secondary-button inline-flex items-center gap-1.5" :disabled="loadingSessions" @click="refreshAuthSessions()">
              <RefreshCw :size="13" aria-hidden="true" />
              <span>刷新</span>
            </button>
            <button
              v-if="otherSessionCount > 0"
              type="button"
              class="settings-secondary-button sec-danger-btn inline-flex items-center gap-1.5"
              @click="confirmRevokeOthersOpen = true"
            >
              <LogOut :size="13" aria-hidden="true" />
              <span>移除其他 {{ otherSessionCount }} 台</span>
            </button>
          </div>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="lan" title="局域网访问">
    <div class="settings-group">
      <SettingsItem
        title="允许局域网连接"
        :desc="isWildcardBind ? '已通过 --listen 全网卡监听' : undefined"
      >
        <input v-model="lanToggleModel" :disabled="isWildcardBind" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <SettingsItem
        v-if="lanUrls.length"
        as="div"
        stacked
        title="连接地址（点击复制）"
      >
        <div class="flex flex-wrap gap-2">
          <button
            v-for="url in lanUrls"
            :key="url"
            type="button"
            class="settings-secondary-button font-mono text-xs"
            @click="copyText(url)"
          >
            {{ url }}
          </button>
        </div>
      </SettingsItem>

      <SettingsItem
        v-if="isAndroidApp"
        as="div"
        title="当前服务器"
        :desc="androidServerLabel"
      >
        <button type="button" class="settings-secondary-button" @click="openAndroidServerSwitcher()">
          切换
        </button>
      </SettingsItem>
      <SettingsItem title="监听地址与端口">
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
          <span v-if="isSettingLocked('bindHost') || isSettingLocked('port')" class="settings-desc">
            由启动参数 --listen 锁定
          </span>
          <span v-if="settingsFieldErrors.bindHost || settingsFieldErrors.port" class="settings-field-error">
            {{ settingsFieldErrors.bindHost ?? settingsFieldErrors.port }}
          </span>
        </div>
      </SettingsItem>

      <SettingsItem stacked title="CORS 允许来源">
        <input :value="settingsForm.corsOrigins.join(', ')" class="settings-input settings-input-wide" placeholder="逗号分隔" @input="settingsForm.corsOrigins = parseList($event)" />
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="token" title="API 与 MCP">
    <div class="settings-group">
      <SettingsItem title="公网开放 MCP">
        <input v-model="settingsForm.exposeMcpPublicly" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <SettingsItem as="div" stacked title="访问令牌">
        <div class="flex w-full min-w-0 flex-col gap-2 sm:flex-row">
          <div class="sec-password-wrap min-w-0 flex-1">
            <input
              v-model="settingsForm.token"
              :type="showApiToken ? 'text' : 'password'"
              aria-label="API 访问令牌"
              class="settings-input w-full pr-10 font-mono text-xs"
              autocomplete="off"
              autocapitalize="off"
              autocorrect="off"
              spellcheck="false"
            />
            <button
              type="button"
              class="sec-password-toggle"
              :aria-label="showApiToken ? '隐藏令牌' : '显示令牌'"
              @click="showApiToken = !showApiToken"
            >
              <EyeOff v-if="showApiToken" :size="15" aria-hidden="true" />
              <Eye v-else :size="15" aria-hidden="true" />
            </button>
          </div>
          <button type="button" class="settings-secondary-button" :disabled="!settingsForm.token.trim()" @click="copyText(settingsForm.token)">复制</button>
          <button type="button" class="settings-secondary-button" :disabled="rotatingToken" @click="confirmRotateTokenOpen = true">重置</button>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <AppDialog
    v-if="confirmRotateTokenOpen"
    title="确认重置访问令牌？"
    :dismissible="!rotatingToken"
    @close="confirmRotateTokenOpen = false"
  >
    <p class="text-sm leading-6 text-[var(--muted)]">
      重置后旧的 API 访问令牌会<strong class="text-[var(--ink)]">立即失效</strong>，已接入的 MCP 客户端或外部脚本需要更新为新令牌。
    </p>
    <template #footer>
      <button type="button" class="btn-ghost" :disabled="rotatingToken" @click="confirmRotateTokenOpen = false">取消</button>
      <button type="button" class="btn-danger" :disabled="rotatingToken" @click="confirmRotateToken()">
        {{ rotatingToken ? "重置中…" : "确认重置" }}
      </button>
    </template>
  </AppDialog>

  <AppDialog
    v-if="pendingRevokeSession"
    :title="pendingRevokeSession.current ? '确认退出当前设备？' : '确认移除登录设备？'"
    :dismissible="!revokingSession"
    @close="pendingRevokeSession = null"
  >
    <p class="text-sm leading-6 text-[var(--muted)]">
      将立即注销 <strong class="text-[var(--ink)]">{{ pendingRevokeSession.deviceName || "未知设备" }}</strong>
      <template v-if="pendingRevokeSession.ipAddress">（{{ pendingRevokeSession.ipAddress }}）</template>
      的登录状态，该设备需要重新验证才能访问。
    </p>
    <template #footer>
      <button type="button" class="btn-ghost" :disabled="revokingSession" @click="pendingRevokeSession = null">取消</button>
      <button type="button" class="btn-danger" :disabled="revokingSession" @click="confirmRevokeSingleSession()">
        {{ revokingSession ? "处理中…" : pendingRevokeSession.current ? "确认退出" : "确认移除" }}
      </button>
    </template>
  </AppDialog>

  <AppDialog
    v-if="confirmRevokeOthersOpen"
    title="确认移除其他全部设备？"
    :dismissible="!revokingSession"
    @close="confirmRevokeOthersOpen = false"
  >
    <p class="text-sm leading-6 text-[var(--muted)]">
      将强制注销除当前设备外的其余 <strong class="text-[var(--ink)]">{{ otherSessionCount }} 台</strong> 登录设备，这些设备需要重新登录。
    </p>
    <template #footer>
      <button type="button" class="btn-ghost" :disabled="revokingSession" @click="confirmRevokeOthersOpen = false">取消</button>
      <button type="button" class="btn-danger" :disabled="revokingSession" @click="confirmRevokeOthers()">
        {{ revokingSession ? "处理中…" : "确认移除" }}
      </button>
    </template>
  </AppDialog>
</template>

<style scoped>
.sec-password-wrap {
  position: relative;
  display: flex;
  align-items: center;
}

.sec-password-toggle {
  position: absolute;
  right: 5px;
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  color: var(--muted);
  transition: background 150ms ease, color 150ms ease;
}

.sec-password-toggle:hover {
  background: var(--panel);
  color: var(--ink);
}

.sec-session-list {
  display: grid;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.sec-session-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 12px;
  border: 1px solid var(--line);
  background: var(--tint-1);
}

.sec-session-main {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.sec-session-name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  color: var(--ink);
}

.sec-session-tag {
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 11px;
  font-weight: 550;
}

.sec-session-meta {
  font-size: 12px;
  color: var(--muted);
}

.sec-session-ua {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--muted);
}

.sec-danger-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.sec-danger-btn:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--err) 45%, transparent);
  background: var(--err-soft);
  color: var(--err);
}
</style>
