<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { Copy, Eye, EyeOff, RefreshCw } from "@lucide/vue";

import { createQrLoginTicket, fetchRemoteQrSvg } from "../../api";
import { copyText } from "../../lib/actions";
import {
  adminUsername,
  dianaAuthAvailable,
  githubOAuthAvailable,
  googleOAuthAvailable,
  oidcAvailable,
  oidcDisplayName,
  passwordConfigured,
  recentSavedPassword,
  status
} from "../../stores/session";
import { tunnelStatus } from "../../stores/tunnel";

interface AccessUrlOption {
  url: string;
  label: string;
  badge: string;
  isTunnel: boolean;
}

type QrMode = "ticket" | "autofill";

const QR_MAX_UTF8_BYTES = 271;

const tunnelPublicUrl = computed(() => (status.value?.tunnelPublicUrl || tunnelStatus.value?.publicUrl || "").trim());

const urlOptions = computed<AccessUrlOption[]>(() => {
  const options: AccessUrlOption[] = [];
  const tunnelUrl = tunnelPublicUrl.value;
  if (tunnelUrl) {
    options.push({
      url: tunnelUrl,
      label: "公网隧道",
      badge: "外网 HTTPS",
      isTunnel: true
    });
  }
  for (const raw of status.value?.lanUrls ?? []) {
    const trimmed = raw.trim();
    if (!trimmed || trimmed === tunnelUrl) {
      continue;
    }
    options.push({
      url: trimmed,
      label: trimmed.replace(/^https?:\/\//, ""),
      badge: "局域网",
      isTunnel: false
    });
  }
  return options;
});

const selectedUrl = ref("");
const qrMode = ref<QrMode>("ticket");
const autofillPassword = ref(recentSavedPassword.value);
const showAutofillPassword = ref(false);

const ticketCode = ref("");
const ticketUsername = ref("");
const ticketExpiresAt = ref(0);
const ticketBusy = ref(false);
const ticketError = ref("");

const qrSvg = ref("");
const qrLoading = ref(false);
const qrError = ref("");

let lastSeenTunnelUrl = "";
let qrRequestSeq = 0;
let ticketRequestSeq = 0;
let ticketRefreshTimer: number | null = null;
let autofillDebounceTimer: number | null = null;

function clearTicketTimer(): void {
  if (ticketRefreshTimer !== null) {
    window.clearTimeout(ticketRefreshTimer);
    ticketRefreshTimer = null;
  }
}

function clearAutofillTimer(): void {
  if (autofillDebounceTimer !== null) {
    window.clearTimeout(autofillDebounceTimer);
    autofillDebounceTimer = null;
  }
}

onBeforeUnmount(() => {
  clearTicketTimer();
  clearAutofillTimer();
});

watch(recentSavedPassword, (next) => {
  if (next) {
    autofillPassword.value = next;
  }
});

watch(
  urlOptions,
  (options) => {
    const currentTunnel = options.find((item) => item.isTunnel)?.url ?? "";
    const tunnelJustAppeared = Boolean(currentTunnel && currentTunnel !== lastSeenTunnelUrl);
    lastSeenTunnelUrl = currentTunnel;

    if (!options.length) {
      selectedUrl.value = "";
      return;
    }
    if (tunnelJustAppeared || !options.some((item) => item.url === selectedUrl.value)) {
      selectedUrl.value = options[0]!.url;
    }
  },
  { immediate: true }
);

async function refreshQrTicket(): Promise<void> {
  if (!selectedUrl.value) {
    return;
  }
  ticketRequestSeq += 1;
  const seq = ticketRequestSeq;
  ticketBusy.value = true;
  ticketError.value = "";
  clearTicketTimer();
  try {
    const res = await createQrLoginTicket();
    if (seq !== ticketRequestSeq) {
      return;
    }
    ticketCode.value = res.code;
    ticketUsername.value = res.username;
    const ttlMs = Math.max(60, res.expiresInSeconds || 600) * 1000;
    ticketExpiresAt.value = Date.now() + ttlMs;
    ticketRefreshTimer = window.setTimeout(() => {
      if (qrMode.value === "ticket" && selectedUrl.value) {
        void refreshQrTicket();
      }
    }, Math.max(30_000, ttlMs - 30_000));
  } catch (error) {
    if (seq !== ticketRequestSeq) {
      return;
    }
    ticketCode.value = "";
    ticketError.value = error instanceof Error ? error.message : "生成免密凭据失败";
  } finally {
    if (seq === ticketRequestSeq) {
      ticketBusy.value = false;
    }
  }
}

watch(
  [selectedUrl, qrMode],
  ([url, mode]) => {
    if (!url || mode !== "ticket") {
      return;
    }
    if (!ticketCode.value || Date.now() >= ticketExpiresAt.value - 15_000) {
      void refreshQrTicket();
    }
  },
  { immediate: true }
);

watch(adminUsername, (next, prev) => {
  if (next && next !== prev && selectedUrl.value && qrMode.value === "ticket") {
    void refreshQrTicket();
  }
});

const qrTargetUrl = computed(() => {
  const base = selectedUrl.value.trim().replace(/\/+$/g, "");
  if (!base) {
    return "";
  }
  if (qrMode.value === "ticket") {
    if (!ticketCode.value) {
      return "";
    }
    const user = ticketUsername.value || adminUsername.value.trim();
    const params = [`login=${encodeURIComponent(ticketCode.value)}`];
    if (user) {
      params.push(`u=${encodeURIComponent(user)}`);
    }
    return `${base}/?${params.join("&")}`;
  }
  const user = adminUsername.value.trim();
  const pass = autofillPassword.value;
  const hashParts: string[] = [];
  if (user) {
    hashParts.push(`u=${encodeURIComponent(user)}`);
  }
  if (pass) {
    hashParts.push(`p=${encodeURIComponent(pass)}`);
  }
  return hashParts.length ? `${base}/#${hashParts.join("&")}` : `${base}/`;
});

async function loadQrSvg(targetUrl: string): Promise<void> {
  qrRequestSeq += 1;
  const seq = qrRequestSeq;
  if (!targetUrl) {
    qrSvg.value = "";
    qrError.value = ticketError.value;
    qrLoading.value = false;
    return;
  }
  const byteLen = new TextEncoder().encode(targetUrl).length;
  if (byteLen > QR_MAX_UTF8_BYTES) {
    qrSvg.value = "";
    qrError.value = `内容超出二维码容量上限（${byteLen}/${QR_MAX_UTF8_BYTES} 字节），请缩短密码或改用免密直登`;
    qrLoading.value = false;
    return;
  }
  qrLoading.value = true;
  qrError.value = "";
  try {
    const svg = await fetchRemoteQrSvg(targetUrl);
    if (seq !== qrRequestSeq) {
      return;
    }
    qrSvg.value = svg;
  } catch (error) {
    if (seq !== qrRequestSeq) {
      return;
    }
    qrSvg.value = "";
    qrError.value = error instanceof Error ? error.message : "生成二维码失败";
  } finally {
    if (seq === qrRequestSeq) {
      qrLoading.value = false;
    }
  }
}

watch(
  [qrTargetUrl, qrMode],
  ([nextUrl, mode]) => {
    clearAutofillTimer();
    if (!nextUrl) {
      void loadQrSvg("");
      return;
    }
    if (mode === "autofill") {
      autofillDebounceTimer = window.setTimeout(() => {
        void loadQrSvg(nextUrl);
      }, 180);
      return;
    }
    void loadQrSvg(nextUrl);
  },
  { immediate: true }
);

const activeOption = computed(
  () => urlOptions.value.find((item) => item.url === selectedUrl.value) ?? urlOptions.value[0] ?? null
);

const authBadges = computed(() => {
  const list: Array<{ label: string; accent?: boolean }> = [];
  if (passwordConfigured.value) {
    list.push({ label: "密码保护" });
  }
  if (dianaAuthAvailable.value) {
    list.push({ label: "Diana", accent: true });
  }
  if (googleOAuthAvailable.value) {
    list.push({ label: "Google", accent: true });
  }
  if (githubOAuthAvailable.value) {
    list.push({ label: "GitHub", accent: true });
  }
  if (oidcAvailable.value) {
    list.push({ label: oidcDisplayName.value || "SSO", accent: true });
  }
  return list;
});

function copyQrLink(): void {
  const target = qrTargetUrl.value || selectedUrl.value;
  if (target) {
    copyText(target);
  }
}
</script>

<template>
  <div class="mobile-qr-card">
    <p v-if="!urlOptions.length" class="mobile-qr-empty">
      开启「公网隧道」或「允许局域网连接」后，这里会自动生成供手机浏览器扫码访问的二维码。
    </p>

    <div v-else class="mobile-qr-body">
      <div class="mobile-qr-box" :aria-label="selectedUrl ? `扫码访问 ${selectedUrl}` : '访问二维码'">
        <div v-if="qrSvg" class="mobile-qr-svg" v-html="qrSvg" />
        <span v-else-if="qrLoading || (qrMode === 'ticket' && ticketBusy)" class="mobile-qr-placeholder">生成中…</span>
        <span v-else class="mobile-qr-placeholder text-[var(--err)]">{{ qrError || ticketError || "不可用" }}</span>
      </div>

      <div class="mobile-qr-info">
        <div class="mobile-qr-head">
          <strong>手机扫码访问</strong>
          <span v-if="activeOption" class="mobile-qr-badge" :class="{ 'mobile-qr-badge-https': activeOption.isTunnel }">
            {{ activeOption.badge }}
          </span>
        </div>

        <div class="mobile-qr-mode-row">
          <div class="mobile-qr-modes" role="tablist" aria-label="扫码登录模式">
            <button
              type="button"
              role="tab"
              :aria-selected="qrMode === 'ticket'"
              class="mobile-qr-tab"
              :class="{ 'mobile-qr-tab-active': qrMode === 'ticket' }"
              @click="qrMode = 'ticket'"
            >
              免密直登
            </button>
            <button
              type="button"
              role="tab"
              :aria-selected="qrMode === 'autofill'"
              class="mobile-qr-tab"
              :class="{ 'mobile-qr-tab-active': qrMode === 'autofill' }"
              @click="qrMode = 'autofill'"
            >
              填充账号密码
            </button>
          </div>

          <button
            v-if="qrMode === 'ticket'"
            type="button"
            class="mobile-qr-refresh"
            :disabled="ticketBusy"
            title="重新生成一次性扫码凭据"
            @click="refreshQrTicket()"
          >
            <RefreshCw :size="12" :class="{ 'animate-spin': ticketBusy }" aria-hidden="true" />
            <span>{{ ticketBusy ? "刷新中…" : "10 分钟内有效 · 刷新" }}</span>
          </button>
        </div>

        <div v-if="qrMode === 'autofill'" class="mobile-qr-autofill">
          <div class="mobile-qr-pass-wrap">
            <input
              v-model="autofillPassword"
              class="settings-input w-full pr-9 text-xs"
              :type="showAutofillPassword ? 'text' : 'password'"
              placeholder="输入要预填的密码（留空仅填账号）"
              autocomplete="off"
            />
            <button
              type="button"
              class="mobile-qr-pass-toggle"
              :aria-label="showAutofillPassword ? '隐藏密码' : '显示密码'"
              @click="showAutofillPassword = !showAutofillPassword"
            >
              <EyeOff v-if="showAutofillPassword" :size="14" aria-hidden="true" />
              <Eye v-else :size="14" aria-hidden="true" />
            </button>
          </div>
          <span class="mobile-qr-autofill-hint">
            自动写入 <code>{{ adminUsername || "管理员账号" }}</code>；凭据经本地 <code>#</code> 片段传递，不经过服务器。
          </span>
        </div>

        <div v-if="urlOptions.length > 1" class="mobile-qr-tabs" role="tablist" aria-label="切换扫码地址">
          <button
            v-for="item in urlOptions"
            :key="item.url"
            type="button"
            role="tab"
            :aria-selected="item.url === selectedUrl"
            class="mobile-qr-tab"
            :class="{ 'mobile-qr-tab-active': item.url === selectedUrl }"
            @click="selectedUrl = item.url"
          >
            {{ item.label }}
          </button>
        </div>

        <div class="mobile-qr-url-row">
          <code class="mobile-qr-url">{{ selectedUrl }}</code>
          <button
            type="button"
            class="settings-secondary-button mobile-qr-copy shrink-0"
            :disabled="!selectedUrl"
            @click="copyQrLink()"
          >
            <Copy :size="13" aria-hidden="true" />
            <span>{{ qrMode === "ticket" ? "复制免密链接" : "复制预填链接" }}</span>
          </button>
        </div>

        <div v-if="authBadges.length" class="mobile-qr-auths">
          <span class="mobile-qr-auth-label">登录方式：</span>
          <span
            v-for="badge in authBadges"
            :key="badge.label"
            class="mobile-qr-auth-pill"
            :class="{ 'mobile-qr-auth-pill-accent': badge.accent }"
          >
            {{ badge.label }}
          </span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mobile-qr-card {
  border-radius: 14px;
  border: 1px solid color-mix(in srgb, var(--line) 46%, transparent);
  background: var(--settings-block);
  padding: 0.9rem 1rem;
}

.mobile-qr-empty {
  margin: 0;
  font-size: 0.8125rem;
  line-height: 1.55;
  color: var(--muted);
}

.mobile-qr-body {
  display: flex;
  align-items: center;
  gap: 1rem;
}

.mobile-qr-box {
  width: 148px;
  height: 148px;
  padding: 8px;
  border-radius: 12px;
  background: #ffffff;
  border: 1px solid rgba(17, 24, 39, 0.08);
  flex-shrink: 0;
  display: grid;
  place-items: center;
}

.mobile-qr-svg {
  width: 100%;
  height: 100%;
}

.mobile-qr-svg :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
}

.mobile-qr-placeholder {
  font-size: 11.5px;
  line-height: 1.4;
  color: #6b7280;
  text-align: center;
  padding: 4px;
  word-break: break-word;
}

.mobile-qr-info {
  min-width: 0;
  flex: 1;
  display: grid;
  gap: 0.5rem;
}

.mobile-qr-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.45rem;
  font-size: 0.875rem;
  color: var(--ink);
}

.mobile-qr-badge {
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 11px;
  font-weight: 600;
}

.mobile-qr-badge-https {
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  color: var(--accent);
}

.mobile-qr-mode-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.45rem;
}

.mobile-qr-modes {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 0.3rem;
}

.mobile-qr-refresh {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-size: 0.72rem;
  color: var(--muted);
  transition: color 150ms ease;
}

.mobile-qr-refresh:hover:not(:disabled) {
  color: var(--ink);
}

.mobile-qr-autofill {
  display: grid;
  gap: 0.3rem;
}

.mobile-qr-pass-wrap {
  position: relative;
  display: flex;
  align-items: center;
  max-width: 24rem;
}

.mobile-qr-pass-toggle {
  position: absolute;
  right: 5px;
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: 7px;
  color: var(--muted);
}

.mobile-qr-pass-toggle:hover {
  background: var(--panel);
  color: var(--ink);
}

.mobile-qr-autofill-hint {
  font-size: 0.72rem;
  line-height: 1.4;
  color: var(--muted);
}

.mobile-qr-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.mobile-qr-tab {
  padding: 0.22rem 0.6rem;
  border-radius: 999px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink-secondary);
  font-size: 0.75rem;
  font-weight: 550;
  transition: border-color 150ms ease, color 150ms ease, background 150ms ease;
}

.mobile-qr-tab-active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.mobile-qr-url-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}

.mobile-qr-url {
  min-width: 0;
  flex: 1 1 12rem;
  padding: 0.35rem 0.55rem;
  border-radius: 8px;
  background: var(--panel);
  color: var(--ink);
  font-size: 0.75rem;
  word-break: break-all;
}

.mobile-qr-copy {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}

.mobile-qr-auths {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
}

.mobile-qr-auth-label {
  font-size: 0.75rem;
  color: var(--muted);
}

.mobile-qr-auth-pill {
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 11px;
  font-weight: 550;
}

.mobile-qr-auth-pill-accent {
  background: var(--accent-soft);
  color: var(--accent);
}

@media (max-width: 560px) {
  .mobile-qr-body {
    flex-direction: column;
    align-items: stretch;
  }

  .mobile-qr-box {
    align-self: center;
  }
}
</style>
