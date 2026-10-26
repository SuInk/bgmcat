<script setup lang="ts">
import { onMounted } from "vue";

import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { githubSessions, loadingSessions, refreshAuthSessions, revokeSession, settingsForm } from "../../../stores/session";

function formatSessionTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function parseList(event: Event): string[] {
  return (event.target as HTMLInputElement).value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

onMounted(() => {
  void refreshAuthSessions();
});
</script>

<template>
  <SettingsSection id="github" title="GitHub 登录">
    <div class="settings-group">
      <SettingsItem title="允许用 GitHub 账号登录" desc="从公网打开时可以用 GitHub 登录，不必再粘 token。开启后填写下面几项。">
        <input v-model="settingsForm.githubOAuthEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>

      <template v-if="settingsForm.githubOAuthEnabled">
        <SettingsItem stacked title="GitHub Client ID" desc="GitHub OAuth App 的 Client ID">
          <input v-model="settingsForm.githubClientId" class="settings-input settings-input-wide" autocomplete="off" spellcheck="false" />
        </SettingsItem>

        <SettingsItem stacked title="GitHub Client Secret">
          <template #desc>回调地址填 <code>https://你的域名/api/auth/github/callback</code></template>
          <input v-model="settingsForm.githubClientSecret" class="secret-mask-input settings-input settings-input-wide" autocomplete="off" spellcheck="false" />
        </SettingsItem>

        <SettingsItem stacked title="允许登录的 GitHub 用户名">
          <template #desc>多个用英文逗号分隔，忽略大小写；<strong>留空则无人可登录</strong>。</template>
          <input
            :value="settingsForm.githubAllowedLogins.join(', ')"
            class="settings-input settings-input-wide"
            placeholder="SuInk"
            @input="settingsForm.githubAllowedLogins = parseList($event)"
          />
        </SettingsItem>

        <SettingsItem as="div" stacked title="已登录会话" desc="通过 GitHub 登录的浏览器会话，撤销后该浏览器需要重新登录。">
          <div class="grid w-full min-w-0 gap-2">
            <p v-if="loadingSessions" class="settings-desc">读取中…</p>
            <p v-else-if="!githubSessions.length" class="settings-desc">暂无 GitHub 登录会话。</p>
            <div v-for="session in githubSessions" :key="session.id" class="settings-record">
              <span class="min-w-0 text-sm text-[var(--ink)]">
                <strong>{{ session.githubLogin }}</strong>
                <span v-if="session.current" class="ml-2 text-xs text-[var(--muted)]">（当前浏览器）</span>
                <span class="settings-record-meta">
                  登录于 {{ formatSessionTime(session.createdAt) }} · 最近活跃 {{ formatSessionTime(session.lastSeenAt) }}
                </span>
              </span>
              <button type="button" class="settings-secondary-button" @click="revokeSession(session.id)">撤销</button>
            </div>
          </div>
        </SettingsItem>
      </template>
    </div>
  </SettingsSection>
</template>
