<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Eye, EyeOff, KeyRound, LogIn, Monitor, Moon, ShieldCheck, Sun } from "@lucide/vue";

import BrandLogo from "../components/common/BrandLogo.vue";
import {
  ApiRequestError,
  claimDianaPairing,
  createDianaPairing,
  isAndroidAppRuntime,
  isBackendUnreachable,
  openAndroidServerSwitcher,
  pollDianaPairing,
  readAndroidServerLabel
} from "../api";
import { openExternalLink } from "../lib/actions";
import { readAutofillFromLocation } from "../stores/router";
import {
  authCallbackError,
  completeDianaSessionResult,
  dianaAuthAvailable,
  dianaOwnerPairAvailable,
  githubOAuthAvailable,
  googleOAuthAvailable,
  loginWithDianaPassword,
  loginWithPassword,
  oidcAvailable,
  oidcDisplayName,
  prefillPassword,
  prefillUsername,
  startGithubLogin,
  startGoogleLogin,
  startOidcLogin
} from "../stores/session";
import { setThemeMode, themeMode } from "../stores/theme";

const emit = defineEmits<{
  success: [];
  unreachable: [detail: string];
}>();

const username = ref("");
const password = ref("");
const showPassword = ref(false);
const loading = ref(false);
const errorMessage = ref("");
const isAndroidApp = isAndroidAppRuntime();
const androidServerLabel = computed(() => (isAndroidApp ? readAndroidServerLabel() : ""));

const hasAnyFederatedAuth = computed(
  () => dianaAuthAvailable.value || googleOAuthAvailable.value || githubOAuthAvailable.value || oidcAvailable.value
);

const dianaPanelOpen = ref(false);
const dianaModeTouched = ref(false);
const dianaMode = ref<"pair" | "password">(dianaOwnerPairAvailable.value ? "pair" : "password");
const dianaBusy = ref(false);
const dianaUsername = ref("");
const dianaPassword = ref("");
const pairCode = ref("");
const pairPollToken = ref("");
const pairSecondsLeft = ref(0);
const claimCode = ref("");

let pairCountdownTimer: number | null = null;
let pairPollTimer: number | null = null;

function clearPairTimers(): void {
  if (pairCountdownTimer !== null) {
    window.clearInterval(pairCountdownTimer);
    pairCountdownTimer = null;
  }
  if (pairPollTimer !== null) {
    window.clearInterval(pairPollTimer);
    pairPollTimer = null;
  }
}

watch(dianaOwnerPairAvailable, (avail) => {
  if (!dianaModeTouched.value) {
    dianaMode.value = avail ? "pair" : "password";
  }
});

watch(
  authCallbackError,
  (msg) => {
    if (msg && !errorMessage.value) {
      errorMessage.value = msg;
    }
  },
  { immediate: true }
);

watch(
  [prefillUsername, prefillPassword],
  ([nextUser, nextPass]) => {
    if (nextUser && !username.value) {
      username.value = nextUser;
    }
    if (nextPass && !password.value) {
      password.value = nextPass;
    }
  },
  { immediate: true }
);

onMounted(() => {
  const urlAutofill = readAutofillFromLocation();
  if (urlAutofill.username && !username.value) {
    username.value = urlAutofill.username;
  }
  if (urlAutofill.password && !password.value) {
    password.value = urlAutofill.password;
  }
  const urlError = new URLSearchParams(window.location.search).get("authError")?.trim();
  if (urlError && !errorMessage.value) {
    errorMessage.value = urlError;
  }
});

onBeforeUnmount(() => {
  clearPairTimers();
});

const themeLabel = computed(() =>
  themeMode.value === "system" ? "外观：跟随系统" : themeMode.value === "light" ? "外观：浅色" : "外观：深色"
);

function cycleTheme(): void {
  setThemeMode(themeMode.value === "system" ? "light" : themeMode.value === "light" ? "dark" : "system");
}

function handleLoginError(err: unknown, fallback: string): void {
  // 后端没应答时不能报「密码错误」：这次请求根本没人验证过密码。
  if (isBackendUnreachable(err)) {
    emit("unreachable", err instanceof Error ? err.message : "连不上 Bangumi Cat 后端服务");
    return;
  }
  if (err instanceof ApiRequestError) {
    errorMessage.value = err.message;
  } else {
    errorMessage.value = err instanceof Error ? err.message : fallback;
  }
}

function toggleDianaPanel(): void {
  dianaPanelOpen.value = !dianaPanelOpen.value;
  if (!dianaPanelOpen.value) {
    clearPairTimers();
  }
}

function selectDianaMode(mode: "pair" | "password"): void {
  dianaModeTouched.value = true;
  dianaMode.value = mode;
  errorMessage.value = "";
}

async function startDianaPairing(): Promise<void> {
  if (dianaBusy.value || loading.value) {
    return;
  }
  dianaBusy.value = true;
  errorMessage.value = "";
  authCallbackError.value = "";
  clearPairTimers();
  try {
    const res = await createDianaPairing();
    pairCode.value = res.code;
    pairPollToken.value = res.pollToken;
    pairSecondsLeft.value = Math.max(1, Math.round(res.expiresInSeconds || 120));

    pairCountdownTimer = window.setInterval(() => {
      if (pairSecondsLeft.value > 0) {
        pairSecondsLeft.value -= 1;
      }
      if (pairSecondsLeft.value <= 0) {
        clearPairTimers();
        pairPollToken.value = "";
      }
    }, 1000);

    pairPollTimer = window.setInterval(async () => {
      const token = pairPollToken.value;
      if (!token) {
        clearPairTimers();
        return;
      }
      try {
        const statusRes = await pollDianaPairing(token);
        if (statusRes.approved) {
          clearPairTimers();
          pairPollToken.value = "";
          completeDianaSessionResult(statusRes);
          emit("success");
        } else if (statusRes.expired) {
          clearPairTimers();
          pairPollToken.value = "";
          pairSecondsLeft.value = 0;
        }
      } catch (err) {
        if (isBackendUnreachable(err)) {
          clearPairTimers();
          emit("unreachable", err instanceof Error ? err.message : "连不上 Bangumi Cat 后端服务");
        }
      }
    }, 2000);
  } catch (err) {
    handleLoginError(err, "获取 Diana 验证码失败");
  } finally {
    dianaBusy.value = false;
  }
}

async function submitDianaClaim(): Promise<void> {
  const code = claimCode.value.trim();
  if (dianaBusy.value || loading.value || !code) {
    return;
  }
  dianaBusy.value = true;
  errorMessage.value = "";
  authCallbackError.value = "";
  try {
    const res = await claimDianaPairing(code);
    clearPairTimers();
    claimCode.value = "";
    completeDianaSessionResult(res);
    emit("success");
  } catch (err) {
    handleLoginError(err, "Diana 验证码登录失败");
  } finally {
    dianaBusy.value = false;
  }
}

async function submitDianaPassword(): Promise<void> {
  if (dianaBusy.value || loading.value || !dianaUsername.value.trim() || !dianaPassword.value) {
    return;
  }
  dianaBusy.value = true;
  errorMessage.value = "";
  authCallbackError.value = "";
  try {
    await loginWithDianaPassword(dianaUsername.value, dianaPassword.value);
    dianaPassword.value = "";
    emit("success");
  } catch (err) {
    handleLoginError(err, "Diana 账号登录失败");
  } finally {
    dianaBusy.value = false;
  }
}

async function submit(): Promise<void> {
  if (loading.value || dianaBusy.value || !username.value.trim() || !password.value) {
    return;
  }
  loading.value = true;
  errorMessage.value = "";
  authCallbackError.value = "";
  try {
    await loginWithPassword(username.value, password.value);
    password.value = "";
    emit("success");
  } catch (err) {
    handleLoginError(err, "登录失败");
  } finally {
    loading.value = false;
  }
}

function beginRedirectOAuth(fn: () => void): void {
  errorMessage.value = "";
  authCallbackError.value = "";
  fn();
}
</script>

<template>
  <div class="login-screen">
    <form class="login-card" @submit.prevent="submit">
      <div class="login-brand">
        <BrandLogo :size="48" class="login-logo-img" />
        <div class="login-brand-copy">
          <strong>Bangumi Cat</strong>
          <span>猫追番</span>
        </div>
      </div>

      <div v-if="isAndroidApp" class="flex items-center justify-between gap-2 rounded-lg border border-[var(--line)] bg-[var(--panel)] px-3 py-2 text-xs">
        <span class="truncate text-[var(--muted)]">服务器：<strong class="font-semibold text-[var(--ink)]">{{ androidServerLabel }}</strong></span>
        <button type="button" class="shrink-0 font-semibold text-[var(--accent)]" @click="openAndroidServerSwitcher()">
          切换
        </button>
      </div>

      <div class="login-field">
        <label for="login-username">管理员账号</label>
        <input
          id="login-username"
          v-model="username"
          class="login-input"
          type="text"
          autocomplete="username"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
          autofocus
          :disabled="loading"
        />
      </div>

      <div class="login-field">
        <label for="login-password">密码</label>
        <div class="password-field">
          <input
            id="login-password"
            v-model="password"
            class="login-input"
            :type="showPassword ? 'text' : 'password'"
            autocomplete="current-password"
            :disabled="loading"
          />
          <button
            class="password-toggle"
            type="button"
            :aria-label="showPassword ? '隐藏密码' : '显示密码'"
            :title="showPassword ? '隐藏密码' : '显示密码'"
            @click="showPassword = !showPassword"
          >
            <EyeOff v-if="showPassword" :size="16" aria-hidden="true" />
            <Eye v-else :size="16" aria-hidden="true" />
          </button>
        </div>
      </div>

      <button
        class="btn-primary login-submit"
        type="submit"
        :disabled="loading || dianaBusy || username.trim().length === 0 || password.length === 0"
      >
        <LogIn :size="15" aria-hidden="true" />
        <span>{{ loading ? "验证中…" : "登录" }}</span>
      </button>

      <p v-if="errorMessage" class="login-error" role="alert">{{ errorMessage }}</p>

      <div v-if="hasAnyFederatedAuth" class="login-divider">
        <span>或快捷登录</span>
      </div>

      <div v-if="hasAnyFederatedAuth" class="login-oauth-list">
        <template v-if="dianaAuthAvailable">
          <button
            class="btn-ghost login-oauth-btn"
            :class="{ 'login-oauth-btn-active': dianaPanelOpen }"
            type="button"
            :disabled="loading || dianaBusy"
            @click="toggleDianaPanel"
          >
            <KeyRound :size="15" aria-hidden="true" />
            <span>使用 Diana 快捷登录</span>
          </button>

          <div v-if="dianaPanelOpen" class="diana-panel">
            <div v-if="dianaOwnerPairAvailable" class="diana-mode-tabs" role="tablist">
              <button
                type="button"
                role="tab"
                :aria-selected="dianaMode === 'pair'"
                class="diana-mode-tab"
                :class="{ 'diana-mode-tab-active': dianaMode === 'pair' }"
                @click="selectDianaMode('pair')"
              >
                私聊验证码
              </button>
              <button
                type="button"
                role="tab"
                :aria-selected="dianaMode === 'password'"
                class="diana-mode-tab"
                :class="{ 'diana-mode-tab-active': dianaMode === 'password' }"
                @click="selectDianaMode('password')"
              >
                Diana 密码
              </button>
            </div>

            <div v-if="dianaMode === 'pair' && dianaOwnerPairAvailable" class="diana-mode-body">
              <button
                type="button"
                class="btn-ghost login-oauth-btn"
                :disabled="loading || dianaBusy"
                @click="startDianaPairing"
              >
                <span>{{ pairCode ? "重新生成验证码" : "获取 6 位验证码" }}</span>
              </button>

              <div v-if="pairCode" class="diana-pair-box">
                <code class="diana-pair-code">{{ pairCode }}</code>
                <span class="diana-pair-hint">
                  {{
                    pairSecondsLeft > 0
                      ? `${pairSecondsLeft}s 后过期 · 发给 Diana 机器人私聊确认登录`
                      : "验证码已过期，请重新获取"
                  }}
                </span>
              </div>

              <div class="diana-claim-row">
                <input
                  v-model="claimCode"
                  maxlength="6"
                  inputmode="numeric"
                  class="login-input"
                  placeholder="填入私聊拿到的 6 位码"
                  :disabled="loading || dianaBusy"
                  @keydown.enter.stop.prevent="submitDianaClaim"
                />
                <button
                  type="button"
                  class="btn-primary diana-claim-btn"
                  :disabled="loading || dianaBusy || claimCode.trim().length < 6"
                  @click="submitDianaClaim"
                >
                  验证
                </button>
              </div>
            </div>

            <div v-else class="diana-mode-body">
              <input
                v-model="dianaUsername"
                class="login-input"
                type="text"
                placeholder="Diana 账号"
                autocomplete="off"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                :disabled="loading || dianaBusy"
                @keydown.enter.stop.prevent="submitDianaPassword"
              />
              <input
                v-model="dianaPassword"
                class="login-input"
                type="password"
                placeholder="Diana 密码"
                autocomplete="off"
                :disabled="loading || dianaBusy"
                @keydown.enter.stop.prevent="submitDianaPassword"
              />
              <button
                type="button"
                class="btn-primary login-submit"
                :disabled="loading || dianaBusy || !dianaUsername.trim() || !dianaPassword"
                @click="submitDianaPassword"
              >
                <span>{{ dianaBusy ? "验证中…" : "使用 Diana 账号登录" }}</span>
              </button>
            </div>
          </div>
        </template>

        <button
          v-if="googleOAuthAvailable"
          class="btn-ghost login-oauth-btn"
          type="button"
          :disabled="loading || dianaBusy"
          @click="beginRedirectOAuth(startGoogleLogin)"
        >
          <svg viewBox="0 0 18 18" width="15" height="15" aria-hidden="true">
            <path
              fill="#4285F4"
              d="M17.64 9.2c0-.64-.06-1.25-.16-1.84H9v3.48h4.84a4.14 4.14 0 0 1-1.8 2.72v2.26h2.92a8.78 8.78 0 0 0 2.68-6.62Z"
            />
            <path
              fill="#34A853"
              d="M9 18c2.43 0 4.47-.8 5.96-2.18l-2.92-2.26c-.8.54-1.84.86-3.04.86-2.34 0-4.33-1.58-5.04-3.7H.95v2.33A9 9 0 0 0 9 18Z"
            />
            <path
              fill="#FBBC05"
              d="M3.96 10.72a5.4 5.4 0 0 1 0-3.44V4.95H.95a9 9 0 0 0 0 8.1l3.01-2.33Z"
            />
            <path
              fill="#EA4335"
              d="M9 3.58c1.32 0 2.5.45 3.44 1.35l2.58-2.59C13.46.89 11.43 0 9 0A9 9 0 0 0 .95 4.95l3.01 2.33C4.67 5.16 6.66 3.58 9 3.58Z"
            />
          </svg>
          <span>使用 Google 登录</span>
        </button>

        <button
          v-if="githubOAuthAvailable"
          class="btn-ghost login-oauth-btn"
          type="button"
          :disabled="loading || dianaBusy"
          @click="beginRedirectOAuth(startGithubLogin)"
        >
          <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
            <path
              fill="currentColor"
              d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
            />
          </svg>
          <span>使用 GitHub 登录</span>
        </button>

        <button
          v-if="oidcAvailable"
          class="btn-ghost login-oauth-btn"
          type="button"
          :disabled="loading || dianaBusy"
          @click="beginRedirectOAuth(startOidcLogin)"
        >
          <ShieldCheck :size="15" aria-hidden="true" />
          <span>使用 {{ oidcDisplayName || "SSO" }} 登录</span>
        </button>
      </div>
    </form>

    <nav class="login-links" aria-label="快捷入口">
      <button
        type="button"
        class="login-icon-link"
        :title="themeLabel"
        :aria-label="themeLabel"
        @click="cycleTheme"
      >
        <Moon v-if="themeMode === 'dark'" :size="17" aria-hidden="true" />
        <Sun v-else-if="themeMode === 'light'" :size="17" aria-hidden="true" />
        <Monitor v-else :size="17" aria-hidden="true" />
      </button>
      <button
        type="button"
        class="login-icon-link"
        title="GitHub 源码仓库"
        aria-label="GitHub 源码仓库"
        @click="openExternalLink('https://github.com/SuInk/bgmcat')"
      >
        <svg viewBox="0 0 16 16" width="17" height="17" aria-hidden="true">
          <path
            fill="currentColor"
            d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
          />
        </svg>
      </button>
    </nav>
  </div>
</template>

<style scoped>
/* 对齐 Diana 控制台登录页骨架 (.login-screen / .login-card / .password-field) */
.login-screen {
  position: relative;
  min-height: 100dvh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  background: var(--shell-bg);
  padding: 24px;
}

.login-card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: min(368px, 100%);
  padding: 32px 28px;
  border-radius: var(--radius-lg, 18px);
  background: var(--card);
  border: 1px solid var(--line);
  box-shadow: var(--strong-shadow);
}

.login-brand {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  text-align: center;
}

.login-brand-copy strong {
  display: block;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.login-brand-copy span {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  color: var(--muted);
}

.login-subtitle {
  margin: -4px 0 4px;
  text-align: center;
  font-size: 13px;
  color: var(--muted);
}

.login-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.login-field label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-secondary);
}

.login-input {
  width: 100%;
  height: 38px;
  padding: 0 12px;
  border-radius: var(--radius-sm, 10px);
  border: 1px solid var(--line);
  background: var(--input-bg);
  color: var(--ink);
  font-size: 14px;
  outline: none;
  transition: border-color 150ms ease, box-shadow 150ms ease;
}

.login-input:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.login-input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.password-field {
  position: relative;
}

.password-field .login-input {
  padding-right: 42px;
}

.password-toggle {
  position: absolute;
  top: 50%;
  right: 4px;
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
  transform: translateY(-50%);
  transition: background 150ms ease, color 150ms ease;
}

.password-toggle:hover {
  background: var(--panel);
  color: var(--ink);
}

.password-toggle:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}

.login-submit {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  width: 100%;
  height: 38px;
  margin-top: 2px;
  font-size: 14px;
  font-weight: 600;
}

.login-error {
  margin: 0;
  padding: 9px 12px;
  border-radius: var(--radius-sm, 10px);
  border: 1px solid color-mix(in srgb, var(--err) 28%, transparent);
  background: var(--err-soft);
  color: var(--err);
  font-size: 12.5px;
  line-height: 1.5;
  text-align: center;
}

.login-divider {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--muted);
  font-size: 12px;
}

.login-divider::before,
.login-divider::after {
  content: "";
  flex: 1;
  height: 1px;
  background: var(--line);
}

.login-oauth-list {
  display: grid;
  gap: 8px;
}

.login-oauth-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  height: 38px;
  font-size: 13.5px;
  font-weight: 550;
}

.login-oauth-btn-active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.diana-panel {
  display: grid;
  gap: 10px;
  padding: 12px;
  border-radius: var(--radius-sm, 10px);
  border: 1px solid var(--line);
  background: var(--panel);
}

.diana-mode-tabs {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px;
  padding: 3px;
  border-radius: 9px;
  background: var(--card);
  border: 1px solid var(--line);
}

.diana-mode-tab {
  height: 28px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--muted);
  transition: background 150ms ease, color 150ms ease;
}

.diana-mode-tab-active {
  background: var(--accent-soft);
  color: var(--accent);
}

.diana-mode-body {
  display: grid;
  gap: 8px;
}

.diana-pair-box {
  display: grid;
  gap: 4px;
  padding: 10px;
  border-radius: 8px;
  background: var(--card);
  border: 1px solid var(--line);
  text-align: center;
}

.diana-pair-code {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: 0.28em;
  color: var(--accent);
}

.diana-pair-hint {
  font-size: 11.5px;
  color: var(--muted);
}

.diana-claim-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.diana-claim-row .login-input {
  min-width: 0;
  flex: 1;
}

.diana-claim-btn {
  flex: 0 0 auto;
  height: 38px;
  padding: 0 14px;
  font-size: 13px;
  font-weight: 600;
}

.login-links {
  position: fixed;
  bottom: max(18px, env(safe-area-inset-bottom));
  display: flex;
  align-items: center;
  gap: 6px;
}

.login-icon-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 10px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--muted);
  transition: color 150ms ease, background 150ms ease, border-color 150ms ease;
}

.login-icon-link:hover {
  color: var(--ink);
  background: var(--card);
  border-color: var(--line);
}
</style>
