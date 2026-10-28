<script setup lang="ts">
/**
 * 运行日志。
 * 以前是一块写死黑底的「终端」，按钮挤在右上角，Aria2 每秒一行的进度读数整屏刷，
 * 真正的错误根本找不到。现在：和其他页同一套工具栏（级别筛选 + 搜索），跟随主题配色，
 * 多行长消息默认只露前几行，「跟随最新」开着才自动滚到底。
 */
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from "vue";

import type { LogEntry } from "@shared/index";

import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { copyText } from "../lib/actions";
import { isBusy } from "../stores/busy";
import { logFilePath, logs, refreshLogs } from "../stores/logs";

type LogLevelFilter = "all" | LogEntry["level"];

const LOG_FONT_SIZE_KEY = "anime-rss.logs.font-size";
const MIN_LOG_FONT_SIZE = 10;
const MAX_LOG_FONT_SIZE = 18;
/** 超过这么多行的消息先折起来：Aria2 的下载摘要一段就是十几行 */
const COLLAPSE_LINES = 3;

const levelFilter = ref<LogLevelFilter>("all");
const query = ref("");
const follow = ref(true);
const logFontSize = ref(readLogFontSize());
const outputRef = ref<HTMLElement | null>(null);
const expandedIds = reactive(new Set<number>());
let autoRefreshTimer: number | null = null;

const LEVELS: Array<{ key: LogLevelFilter; label: string }> = [
  { key: "all", label: "全部" },
  { key: "error", label: "错误" },
  { key: "warn", label: "警告" },
  { key: "info", label: "信息" }
];

function readLogFontSize(): number {
  const parsed = Number.parseInt(window.localStorage.getItem(LOG_FONT_SIZE_KEY) ?? "12", 10);
  if (!Number.isFinite(parsed)) {
    return 12;
  }
  return Math.min(MAX_LOG_FONT_SIZE, Math.max(MIN_LOG_FONT_SIZE, parsed));
}

function setLogFontSize(value: number): void {
  logFontSize.value = Math.min(MAX_LOG_FONT_SIZE, Math.max(MIN_LOG_FONT_SIZE, value));
  window.localStorage.setItem(LOG_FONT_SIZE_KEY, String(logFontSize.value));
}

const levelCounts = computed<Record<LogLevelFilter, number>>(() => {
  const counts: Record<LogLevelFilter, number> = { all: logs.value.length, error: 0, warn: 0, info: 0 };
  for (const entry of logs.value) {
    counts[entry.level] = (counts[entry.level] ?? 0) + 1;
  }
  return counts;
});

/** 接口给的是新→旧，日志习惯旧→新往下读 */
const visibleLogs = computed(() => {
  const keyword = query.value.trim().toLowerCase();
  return [...logs.value]
    .reverse()
    .filter((entry) => levelFilter.value === "all" || entry.level === levelFilter.value)
    .filter((entry) => !keyword || entry.message.toLowerCase().includes(keyword));
});

function dayKey(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value.slice(0, 10);
  }
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

function timeLabel(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value.slice(11, 19);
  }
  return [date.getHours(), date.getMinutes(), date.getSeconds()].map((part) => String(part).padStart(2, "0")).join(":");
}

/** 某条是不是当天第一条：换日时插一行日期分隔 */
function startsNewDay(index: number): boolean {
  const entry = visibleLogs.value[index];
  const previous = visibleLogs.value[index - 1];
  return Boolean(entry) && (!previous || dayKey(previous.createdAt) !== dayKey(entry!.createdAt));
}

function messageLines(entry: LogEntry): string[] {
  return entry.message.trim().split(/\r?\n/u);
}

function shownMessage(entry: LogEntry): string {
  const lines = messageLines(entry);
  return expandedIds.has(entry.id) || lines.length <= COLLAPSE_LINES ? lines.join("\n") : lines.slice(0, COLLAPSE_LINES).join("\n");
}

function toggleExpanded(id: number): void {
  if (expandedIds.has(id)) {
    expandedIds.delete(id);
  } else {
    expandedIds.add(id);
  }
}

const LEVEL_PILL: Record<LogEntry["level"], string> = {
  error: "status-pill-err",
  warn: "status-pill-warn",
  info: "status-pill-muted"
};

function scrollToBottom(): void {
  if (outputRef.value) {
    outputRef.value.scrollTop = outputRef.value.scrollHeight;
  }
}

/** 用户往上翻看旧日志时就别再把他拽回底部 */
function handleScroll(): void {
  const element = outputRef.value;
  if (!element) {
    return;
  }
  const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 24;
  if (!atBottom && follow.value) {
    follow.value = false;
  }
}

function restartAutoRefresh(): void {
  if (autoRefreshTimer) {
    window.clearInterval(autoRefreshTimer);
    autoRefreshTimer = null;
  }
  if (follow.value) {
    autoRefreshTimer = window.setInterval(() => void refreshLogs(), 3000);
  }
}

watch(
  () => visibleLogs.value.map((entry) => entry.id).join(","),
  async () => {
    if (follow.value) {
      await nextTick();
      scrollToBottom();
    }
  }
);

watch(follow, async (enabled) => {
  restartAutoRefresh();
  if (enabled) {
    await nextTick();
    scrollToBottom();
  }
});

onMounted(async () => {
  restartAutoRefresh();
  await nextTick();
  scrollToBottom();
});

onUnmounted(() => {
  if (autoRefreshTimer) {
    window.clearInterval(autoRefreshTimer);
  }
});
</script>

<template>
  <section class="page-shell rounded-lg border border-[var(--line)] bg-[var(--table-bg)] shadow-[var(--soft-shadow)]">
    <div class="page-toolbar">
      <div class="toolbar-row">
        <div class="toolbar-chips" role="group" aria-label="按级别筛选">
          <button
            v-for="level in LEVELS"
            :key="level.key"
            type="button"
            class="chip"
            :class="{ 'chip-active': levelFilter === level.key }"
            :aria-pressed="levelFilter === level.key"
            @click="levelFilter = level.key"
          >
            {{ level.label }}
            <span class="chip-count tabular-nums">{{ levelCounts[level.key] }}</span>
          </button>
        </div>
        <button
          type="button"
          class="toolbar-meta hidden min-w-0 max-w-[45%] truncate hover:text-[var(--ink)] sm:block"
          :title="logFilePath ? `${logFilePath}（点击复制）` : undefined"
          :disabled="!logFilePath"
          @click="copyText(logFilePath)"
        >
          {{ logFilePath || "运行时日志" }}
        </button>
      </div>

      <div class="toolbar-row flex-wrap">
        <label class="search-field min-w-[12rem]">
          <AppNavIcon name="search" aria-hidden="true" />
          <input v-model="query" type="search" placeholder="搜索日志内容" aria-label="搜索日志内容" />
        </label>
        <label class="chip shrink-0 cursor-pointer px-3 py-1.5 text-sm" :class="{ 'chip-active': follow }">
          <input v-model="follow" type="checkbox" class="sr-only" />
          <span class="size-1.5 rounded-full" :class="follow ? 'animate-pulse bg-current' : 'bg-[var(--muted)]'"></span>
          跟随最新
        </label>
        <div class="flex shrink-0 items-center gap-1" role="group" aria-label="字号">
          <button
            type="button"
            class="icon-btn text-xs font-bold"
            :disabled="logFontSize <= MIN_LOG_FONT_SIZE"
            title="减小字号"
            @click="setLogFontSize(logFontSize - 1)"
          >
            A-
          </button>
          <span class="w-9 text-center text-xs font-semibold tabular-nums text-[var(--muted)]">{{ logFontSize }}px</span>
          <button
            type="button"
            class="icon-btn text-xs font-bold"
            :disabled="logFontSize >= MAX_LOG_FONT_SIZE"
            title="增大字号"
            @click="setLogFontSize(logFontSize + 1)"
          >
            A+
          </button>
        </div>
        <button type="button" class="icon-btn shrink-0" :disabled="isBusy('logs')" title="立即刷新" aria-label="立即刷新" @click="refreshLogs()">
          <AppNavIcon name="refresh" :class="{ 'animate-spin': isBusy('logs') }" />
        </button>
      </div>
    </div>

    <div class="min-h-0 px-4 pb-4">
      <div
        ref="outputRef"
        class="log-output h-full overflow-auto rounded-lg border border-[var(--line)] bg-[var(--card)] font-mono"
        :style="{ fontSize: `${logFontSize}px` }"
        @scroll.passive="handleScroll"
      >
        <div v-if="!visibleLogs.length" class="empty-state h-full font-sans">
          <div class="grid max-w-sm gap-2">
            <AppNavIcon name="logs" class="mx-auto size-8 text-[var(--muted)]" aria-hidden="true" />
            <p class="text-sm font-semibold text-[var(--ink)]">{{ logs.length ? "没有符合条件的日志" : "还没有日志" }}</p>
            <p v-if="!logs.length" class="text-xs text-[var(--muted)]">订阅刷新、下载、云盘归档等后台动作会记录在这里。</p>
          </div>
        </div>

        <template v-for="(entry, index) in visibleLogs" :key="entry.id">
          <div v-if="startsNewDay(index)" class="log-day">{{ dayKey(entry.createdAt) }}</div>
          <div class="log-line" :class="{ 'log-line-error': entry.level === 'error' }">
            <time class="log-time">{{ timeLabel(entry.createdAt) }}</time>
            <span class="status-pill justify-self-start font-sans" :class="LEVEL_PILL[entry.level]">{{ entry.level }}</span>
            <div class="min-w-0">
              <div class="log-message">{{ shownMessage(entry) }}</div>
              <button
                v-if="messageLines(entry).length > COLLAPSE_LINES"
                type="button"
                class="mt-0.5 font-sans text-[11px] font-semibold text-[var(--accent-vivid)] hover:underline"
                @click="toggleExpanded(entry.id)"
              >
                {{ expandedIds.has(entry.id) ? "收起" : `展开其余 ${messageLines(entry).length - COLLAPSE_LINES} 行` }}
              </button>
            </div>
          </div>
        </template>
      </div>
    </div>
  </section>
</template>

<style scoped>
.log-day {
  position: sticky;
  top: 0;
  z-index: 1;
  border-bottom: 1px solid var(--line);
  background: var(--panel);
  padding: 0.25rem 0.75rem;
  color: var(--muted);
  font-family: inherit;
  font-size: 11px;
  font-weight: 600;
}

.log-line {
  display: grid;
  grid-template-columns: 4.75rem 3.25rem minmax(0, 1fr);
  align-items: start;
  gap: 0.75rem;
  border-bottom: 1px solid color-mix(in srgb, var(--line) 50%, transparent);
  padding: 0.25rem 0.75rem;
  line-height: 1.5;
}

.log-line:hover {
  background: var(--tint-2);
}

.log-line-error {
  background: color-mix(in srgb, var(--err) 5%, transparent);
}

.log-time {
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  user-select: none;
}

.log-message {
  color: var(--ink);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

/* 手机：时间和级别一行，消息另起一行占满宽度；以前时间单占一列，消息被挤成一行十几个字 */
@media (max-width: 640px) {
  .log-line {
    grid-template-columns: auto auto minmax(0, 1fr);
    gap: 0.125rem 0.5rem;
    padding: 0.375rem 0.75rem;
  }

  .log-line > .min-w-0 {
    grid-column: 1 / -1;
  }
}
</style>
