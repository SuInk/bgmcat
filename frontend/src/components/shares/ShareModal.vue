<script setup lang="ts">
import { computed, ref, watch } from "vue";

import type { ShareRecord } from "../../api";
import { formatBytes } from "../../lib/format";
import { selectTab } from "../../stores/boot";
import {
  activeShareOrigin,
  activeSharesCount,
  availableShareOrigins,
  buildShareUrl,
  closeShareModal,
  copyTextToClipboard,
  expiredSharesCount,
  formatShareCopyText,
  removeExpiredShares,
  revokeShareByCode,
  selectedOriginOverride,
  shareDraft,
  shareModalOpen,
  shareModalTab,
  shares,
  sharesLoading,
  submitCreateShare,
  toggleShareAllowDownload
} from "../../stores/shares";
import AppDialog from "../common/AppDialog.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";

const EXPIRY_OPTIONS = [
  { label: "1 天", hours: 24 },
  { label: "7 天", hours: 168 },
  { label: "30 天", hours: 720 },
  { label: "永久", hours: 0 }
] as const;

const scope = ref<"single" | "series">("single");
const titleInput = ref("");
const titleTouched = ref(false);
const expiresInHours = ref<number>(168);
const passcode = ref("");
const allowDownload = ref(true);
const creating = ref(false);
const createdRecord = ref<ShareRecord | null>(null);

const hasSeriesOption = computed(
  () => Boolean(shareDraft.value?.seriesTargets && shareDraft.value.seriesTargets.length > 1)
);

const activeTargets = computed(() => {
  const draft = shareDraft.value;
  if (!draft) return [];
  if (scope.value === "series" && draft.seriesTargets?.length) {
    return draft.seriesTargets;
  }
  return draft.singleTargets;
});

function syncDraftForm(): void {
  const draft = shareDraft.value;
  createdRecord.value = null;
  titleTouched.value = false;
  expiresInHours.value = 168;
  passcode.value = "";
  allowDownload.value = true;
  if (!draft) {
    scope.value = "single";
    titleInput.value = "";
    return;
  }
  scope.value = draft.defaultScope === "series" && draft.seriesTargets?.length ? "series" : "single";
  titleInput.value =
    scope.value === "series" && draft.seriesTitle ? draft.seriesTitle : draft.singleTitle;
}

watch(
  () => [shareModalOpen.value, shareDraft.value] as const,
  ([open]) => {
    if (open) {
      syncDraftForm();
    }
  },
  { immediate: true }
);

function selectScope(nextScope: "single" | "series"): void {
  if (scope.value === nextScope) return;
  scope.value = nextScope;
  createdRecord.value = null;
  if (!titleTouched.value && shareDraft.value) {
    titleInput.value =
      nextScope === "series" && shareDraft.value.seriesTitle
        ? shareDraft.value.seriesTitle
        : shareDraft.value.singleTitle;
  }
}

function generateRandomPasscode(): void {
  const alphabet = "23456789abcdefghjkmnpqrstuvwxyz";
  let code = "";
  const values = new Uint32Array(4);
  crypto.getRandomValues(values);
  for (let i = 0; i < 4; i += 1) {
    code += alphabet[values[i]! % alphabet.length];
  }
  passcode.value = code;
}

const createdShareUrl = computed(() =>
  createdRecord.value ? buildShareUrl(createdRecord.value.code) : ""
);

async function handleCreate(): Promise<void> {
  if (!activeTargets.value.length || creating.value) return;
  creating.value = true;
  try {
    const created = await submitCreateShare({
      title: titleInput.value.trim(),
      expiresInHours: expiresInHours.value,
      passcode: passcode.value.trim(),
      allowDownload: allowDownload.value,
      targets: activeTargets.value
    });
    if (created) {
      createdRecord.value = created;
      const url = buildShareUrl(created.code);
      await copyTextToClipboard(
        formatShareCopyText(created, url),
        created.hasPasscode ? "分享链接与提取码已复制" : "分享链接已复制"
      );
    }
  } finally {
    creating.value = false;
  }
}

function copyShareItem(record: ShareRecord, urlOnly = false): void {
  const url = buildShareUrl(record.code);
  const content = urlOnly ? url : formatShareCopyText(record, url);
  void copyTextToClipboard(
    content,
    urlOnly ? "分享链接已复制" : record.hasPasscode ? "链接与提取码已复制" : "分享链接已复制"
  );
}

function formatExpiryBadge(record: ShareRecord): string {
  if (record.expired) return "已过期";
  if (!record.expiresAt) return "永久有效";
  const diffMs = new Date(record.expiresAt).getTime() - Date.now();
  if (!Number.isFinite(diffMs) || diffMs <= 0) return "已过期";
  const hours = Math.ceil(diffMs / 3_600_000);
  if (hours < 24) return `剩余 ${hours} 小时`;
  const days = Math.ceil(hours / 24);
  return `剩余 ${days} 天`;
}

function jumpToNetworkSettings(): void {
  closeShareModal();
  selectTab("settings");
}
</script>

<template>
  <AppDialog
    v-if="shareModalOpen"
    :title="shareDraft ? '分享与外链管理' : '已分享链接'"
    size="md"
    :dismissible="!creating"
    @close="closeShareModal"
  >
    <template #title>
      <div class="flex flex-wrap items-center gap-2.5">
        <span>{{ shareDraft ? "分享" : "已分享链接" }}</span>
        <div v-if="shareDraft" class="seg-tabs">
          <button
            type="button"
            class="seg-tab"
            :class="{ 'is-active': shareModalTab === 'create' }"
            @click="shareModalTab = 'create'"
          >
            创建链接
          </button>
          <button
            type="button"
            class="seg-tab"
            :class="{ 'is-active': shareModalTab === 'manage' }"
            @click="shareModalTab = 'manage'"
          >
            已分享
            <span v-if="activeSharesCount > 0" class="tabular-nums opacity-75">{{ activeSharesCount }}</span>
          </button>
        </div>
      </div>
    </template>

    <!-- 创建分享页 -->
    <div v-if="shareModalTab === 'create' && shareDraft" class="grid gap-3">
      <!-- 范围切换（单集 vs 整部多集连播） -->
      <div v-if="hasSeriesOption" class="flex items-center justify-between gap-2 rounded-lg border border-[var(--line)] bg-[var(--panel)] px-3 py-2">
        <span class="text-xs font-medium text-[var(--ink-secondary)]">分享范围</span>
        <div class="seg-tabs">
          <button
            type="button"
            class="seg-tab"
            :class="{ 'is-active': scope === 'series' }"
            @click="selectScope('series')"
          >
            整部连播 · {{ shareDraft.seriesTargets?.length }} 集
          </button>
          <button
            type="button"
            class="seg-tab"
            :class="{ 'is-active': scope === 'single' }"
            @click="selectScope('single')"
          >
            {{ shareDraft.singleSubLabel || "当前单集" }}
          </button>
        </div>
      </div>

      <!-- 分享标题 -->
      <label class="grid gap-1">
        <span class="text-xs font-medium text-[var(--ink-secondary)]">分享标题</span>
        <input
          v-model="titleInput"
          type="text"
          maxlength="160"
          class="share-input"
          placeholder="输入访客可见的标题"
          @input="titleTouched = true"
        />
      </label>

      <!-- 有效期 + 提取码 紧凑双栏 -->
      <div class="grid gap-2.5 sm:grid-cols-2">
        <div class="grid gap-1">
          <span class="text-xs font-medium text-[var(--ink-secondary)]">有效期</span>
          <div class="seg-tabs w-full justify-between">
            <button
              v-for="opt in EXPIRY_OPTIONS"
              :key="opt.hours"
              type="button"
              class="seg-tab flex-1 justify-center"
              :class="{ 'is-active': expiresInHours === opt.hours }"
              @click="
                expiresInHours = opt.hours;
                createdRecord = null;
              "
            >
              {{ opt.label }}
            </button>
          </div>
        </div>

        <div class="grid gap-1">
          <div class="flex items-center justify-between">
            <span class="text-xs font-medium text-[var(--ink-secondary)]">提取码（可选）</span>
            <div class="flex items-center gap-1.5 text-[11px]">
              <button
                type="button"
                class="font-medium text-[var(--accent-vivid)] hover:underline"
                @click="
                  generateRandomPasscode();
                  createdRecord = null;
                "
              >
                随机生成
              </button>
              <button
                v-if="passcode"
                type="button"
                class="text-[var(--muted)] hover:text-[var(--ink)]"
                @click="
                  passcode = '';
                  createdRecord = null;
                "
              >
                清空
              </button>
            </div>
          </div>
          <input
            v-model="passcode"
            type="text"
            maxlength="32"
            class="share-input font-mono"
            placeholder="留空免密直接访问"
            @input="createdRecord = null"
          />
        </div>
      </div>

      <!-- 下载权限开关 -->
      <label class="flex cursor-pointer items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--panel)] px-3 py-2">
        <span class="text-xs font-semibold text-[var(--ink)]">允许下载原文件</span>
        <input
          v-model="allowDownload"
          type="checkbox"
          class="size-4 shrink-0 accent-[var(--accent-vivid)]"
          @change="createdRecord = null"
        />
      </label>

      <!-- 链接域名选择（有多地址时展示） -->
      <div v-if="availableShareOrigins.length > 1" class="grid gap-1">
        <span class="text-xs font-medium text-[var(--ink-secondary)]">链接地址</span>
        <div class="flex flex-wrap items-center gap-1.5">
          <button
            v-for="opt in availableShareOrigins"
            :key="opt.key"
            type="button"
            class="origin-pill"
            :class="{ 'is-active': activeShareOrigin?.origin === opt.origin }"
            @click="selectedOriginOverride = opt.origin"
          >
            <span>{{ opt.label }}</span>
            <span class="max-w-[13rem] truncate font-mono text-[11px] opacity-75">{{ opt.origin.replace(/^https?:\/\//, "") }}</span>
          </button>
        </div>
      </div>

      <p
        v-if="activeShareOrigin?.isLoopback"
        class="rounded-lg border border-[var(--line)] bg-[var(--panel)] px-3 py-1.5 text-[11px] text-[var(--muted)]"
      >
        当前为本机地址 ·
        <button type="button" class="font-semibold text-[var(--accent-vivid)] hover:underline" @click="jumpToNetworkSettings">
          开启局域网或公网访问 →
        </button>
      </p>

      <!-- 创建成功结果框 -->
      <div
        v-if="createdRecord"
        class="grid gap-2 rounded-lg border border-[var(--accent-line)] bg-[var(--panel)] p-3"
      >
        <div class="flex items-center justify-between gap-2">
          <span class="inline-flex items-center gap-1.5 text-xs font-semibold text-[var(--ok)]">
            <AppNavIcon name="check" class="size-3.5" aria-hidden="true" />
            分享链接已生成
          </span>
          <a
            :href="createdShareUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 text-xs font-medium text-[var(--accent-vivid)] hover:underline"
          >
            新窗口预览
            <AppNavIcon name="external" class="size-3" aria-hidden="true" />
          </a>
        </div>
        <div class="flex flex-wrap items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--card)] px-2.5 py-1.5 text-xs">
          <span class="min-w-0 flex-1 truncate font-mono text-[var(--ink)] select-all">{{ createdShareUrl }}</span>
          <span
            v-if="createdRecord.hasPasscode"
            class="shrink-0 rounded bg-[var(--panel)] px-1.5 py-0.5 font-mono text-[11px] font-semibold text-[var(--ink)]"
          >
            码 {{ createdRecord.passcode }}
          </span>
        </div>
        <div class="flex flex-wrap items-center justify-end gap-2">
          <button
            v-if="createdRecord.hasPasscode"
            type="button"
            class="btn-ghost px-3 py-1.5 text-xs font-semibold whitespace-nowrap"
            @click="copyShareItem(createdRecord, true)"
          >
            仅复制链接
          </button>
          <button
            type="button"
            class="btn-primary px-3.5 py-1.5 text-xs font-semibold whitespace-nowrap"
            @click="copyShareItem(createdRecord, false)"
          >
            <AppNavIcon name="copy" class="size-3.5 shrink-0" aria-hidden="true" />
            <span>{{ createdRecord.hasPasscode ? "复制链接与提取码" : "复制链接" }}</span>
          </button>
        </div>
      </div>
    </div>

    <!-- 已分享管理页 -->
    <div v-else class="grid gap-2">
      <div
        v-if="sharesLoading && !shares.length"
        class="py-8 text-center text-xs text-[var(--muted)]"
      >
        正在读取分享列表…
      </div>
      <div
        v-else-if="!shares.length"
        class="rounded-lg border border-dashed border-[var(--line)] py-8 text-center text-xs text-[var(--muted)]"
      >
        暂无分享链接
      </div>
      <div v-else class="grid max-h-[22rem] gap-1.5 overflow-y-auto pr-0.5">
        <div
          v-for="item in shares"
          :key="item.code"
          class="share-row"
          :class="{ 'opacity-60': item.expired }"
        >
          <div class="grid min-w-0 flex-1 gap-0.5">
            <div class="flex items-center gap-1.5">
              <a
                :href="buildShareUrl(item.code)"
                target="_blank"
                rel="noopener noreferrer"
                class="truncate text-[13px] font-semibold text-[var(--ink)] hover:text-[var(--accent-vivid)] hover:underline"
              >
                {{ item.title }}
              </a>
              <span
                v-if="item.hasPasscode"
                class="shrink-0 rounded border border-[var(--line)] bg-[var(--card)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--ink-secondary)]"
                :title="`提取码：${item.passcode}`"
              >
                码 {{ item.passcode }}
              </span>
            </div>
            <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-[var(--muted)] tabular-nums">
              <span>{{ item.itemCount > 1 ? `${item.itemCount} 个条目` : "单文件" }}</span>
              <span v-if="item.totalSize > 0">· {{ formatBytes(item.totalSize) }}</span>
              <span>· {{ formatExpiryBadge(item) }}</span>
              <span>· 浏览 {{ item.viewCount }}</span>
            </div>
          </div>

          <div class="flex shrink-0 items-center gap-1">
            <button
              v-if="!item.expired"
              type="button"
              class="mini-action"
              :title="item.allowDownload ? '点击切换为仅限在线播放' : '点击允许访客下载原文件'"
              @click="toggleShareAllowDownload(item)"
            >
              {{ item.allowDownload ? "可下载" : "仅播放" }}
            </button>
            <button
              v-if="!item.expired"
              type="button"
              class="mini-action"
              title="复制分享链接"
              @click="copyShareItem(item, false)"
            >
              复制
            </button>
            <button
              type="button"
              class="mini-action text-[var(--err)] hover:border-[var(--err-soft)] hover:bg-[var(--err-soft)]"
              title="立即撤销此分享链接"
              @click="revokeShareByCode(item.code)"
            >
              撤销
            </button>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <template v-if="shareModalTab === 'create' && shareDraft">
        <button type="button" class="btn-ghost px-3.5 py-1.5 text-xs font-semibold whitespace-nowrap" :disabled="creating" @click="closeShareModal">
          关闭
        </button>
        <button
          type="button"
          class="btn-primary px-4 py-1.5 text-xs font-semibold whitespace-nowrap"
          :disabled="creating || !activeTargets.length"
          @click="handleCreate"
        >
          <AppNavIcon name="link" class="size-3.5 shrink-0" aria-hidden="true" />
          <span>{{ creating ? "生成中…" : createdRecord ? "重新生成新链接" : "创建并复制链接" }}</span>
        </button>
      </template>
      <template v-else>
        <button
          v-if="expiredSharesCount > 0"
          type="button"
          class="btn-ghost mr-auto px-3.5 py-1.5 text-xs font-semibold text-[var(--muted)] whitespace-nowrap"
          @click="removeExpiredShares"
        >
          清理已过期 ({{ expiredSharesCount }})
        </button>
        <button type="button" class="btn-ghost px-3.5 py-1.5 text-xs font-semibold whitespace-nowrap" @click="closeShareModal">
          关闭
        </button>
      </template>
    </template>
  </AppDialog>
</template>

<style scoped>
.seg-tabs {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: 7px;
  background: var(--tint-2);
  border: 1px solid var(--line);
}

.seg-tab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 9px;
  border-radius: 5px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--ink-secondary);
  transition: background-color 0.12s ease, color 0.12s ease;
}

.seg-tab:hover {
  color: var(--ink);
}

.seg-tab.is-active {
  background: var(--card);
  color: var(--ink);
  box-shadow: var(--soft-shadow);
}

.share-input {
  height: 32px;
  width: 100%;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--input-bg);
  padding: 0 10px;
  font-size: 12.5px;
  color: var(--ink);
  outline: none;
  transition: border-color 0.12s ease;
}

.share-input:focus {
  border-color: var(--accent-vivid);
}

.origin-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: var(--panel);
  font-size: 11.5px;
  font-weight: 600;
  color: var(--ink-secondary);
}

.origin-pill.is-active {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent-vivid);
}

.share-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 10px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--panel);
}

.mini-action {
  height: 25px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: var(--card);
  font-size: 11px;
  font-weight: 600;
  color: var(--ink);
  transition: background-color 0.12s ease, border-color 0.12s ease;
}

.mini-action:hover {
  background: var(--tint-2);
}
</style>
