<script setup lang="ts">
/**
 * 运行日志（参考 NapCat WebUI 终端控制台风格）。
 * 统一终端窗口外壳：顶部集成级别筛选、关键词搜索（带高亮）、跟随最新、折行切换、字号与导出；
 * 正文采用高对比度深色终端配色 + 行号槽 + 语义色级别标识 + 悬浮单行复制 + 滚动回到底部按钮；
 * 底部状态栏展示日志来源路径与实时计数。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";

import type { LogEntry } from "@shared/index";

import AppSelect, { type AppSelectOption } from "../components/common/AppSelect.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { copyText } from "../lib/actions";
import { isBusy } from "../stores/busy";
import { logFilePath, logs, refreshLogs } from "../stores/logs";

type LogLevelFilter = "all" | LogEntry["level"];

const LOG_FONT_SIZE_KEY = "bgmcat.logs.font-size";
const LEGACY_LOG_FONT_SIZE_KEY = "anime-rss.logs.font-size";
const LOG_WRAP_KEY = "bgmcat.logs.wrap";
const MIN_LOG_FONT_SIZE = 11;
const MAX_LOG_FONT_SIZE = 17;
const COLLAPSE_LINES = 4;

const levelFilter = ref<LogLevelFilter>("all");
const dateFilter = ref<string>("all");
const query = ref("");
const follow = ref(true);
const wrapLines = ref(readWrapPref());
const logFontSize = ref(readLogFontSize());
const outputRef = ref<HTMLElement | null>(null);
const expandedIds = reactive(new Set<number>());
const copiedId = ref<number | null>(null);
const copiedAll = ref(false);
let copiedTimer: number | null = null;
let autoRefreshTimer: number | null = null;

const LEVELS: Array<{ key: LogLevelFilter; label: string; short: string }> = [
  { key: "all", label: "全部", short: "ALL" },
  { key: "error", label: "错误", short: "ERR" },
  { key: "warn", label: "警告", short: "WARN" },
  { key: "info", label: "信息", short: "INFO" }
];

function readLogFontSize(): number {
  const parsed = Number.parseInt(
    window.localStorage.getItem(LOG_FONT_SIZE_KEY) ?? window.localStorage.getItem(LEGACY_LOG_FONT_SIZE_KEY) ?? "12",
    10
  );
  if (!Number.isFinite(parsed)) {
    return 12;
  }
  return Math.min(MAX_LOG_FONT_SIZE, Math.max(MIN_LOG_FONT_SIZE, parsed));
}

function readWrapPref(): boolean {
  return window.localStorage.getItem(LOG_WRAP_KEY) !== "false";
}

function setLogFontSize(value: number): void {
  logFontSize.value = Math.min(MAX_LOG_FONT_SIZE, Math.max(MIN_LOG_FONT_SIZE, value));
  window.localStorage.setItem(LOG_FONT_SIZE_KEY, String(logFontSize.value));
}

function toggleWrap(): void {
  wrapLines.value = !wrapLines.value;
  window.localStorage.setItem(LOG_WRAP_KEY, String(wrapLines.value));
}

const todayKey = computed(() => dayKey(new Date().toISOString()));

const dateOptions = computed<AppSelectOption[]>(() => {
  const counts = new Map<string, number>();
  for (const entry of logs.value) {
    const key = dayKey(entry.createdAt);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  const days = [...counts.keys()].sort((a, b) => b.localeCompare(a));
  const opts: AppSelectOption[] = [{ value: "all", label: "全部日期" }];
  for (const day of days) {
    const count = counts.get(day) ?? 0;
    const tag = day === todayKey.value ? "今天 · " : "";
    opts.push({ value: day, label: `${day} (${tag}${count})` });
  }
  return opts;
});

const dateScopedLogs = computed(() => {
  if (dateFilter.value === "all") {
    return logs.value;
  }
  return logs.value.filter((entry) => dayKey(entry.createdAt) === dateFilter.value);
});

const levelCounts = computed<Record<LogLevelFilter, number>>(() => {
  const counts: Record<LogLevelFilter, number> = { all: dateScopedLogs.value.length, error: 0, warn: 0, info: 0 };
  for (const entry of dateScopedLogs.value) {
    counts[entry.level] = (counts[entry.level] ?? 0) + 1;
  }
  return counts;
});

/** 接口给的是新→旧，终端习惯旧→新往下读 */
const visibleLogs = computed(() => {
  const keyword = query.value.trim().toLowerCase();
  return [...dateScopedLogs.value]
    .reverse()
    .filter((entry) => levelFilter.value === "all" || entry.level === levelFilter.value)
    .filter((entry) => !keyword || entry.message.toLowerCase().includes(keyword));
});

function handleDateChange(value: string | number): void {
  const next = String(value);
  dateFilter.value = next;
  if (next !== "all" && next !== todayKey.value) {
    follow.value = false;
    void nextTick(() => {
      if (outputRef.value) {
        outputRef.value.scrollTop = 0;
      }
    });
  } else if (next === "all") {
    void nextTick(scrollToBottom);
  }
}

function toggleDayFilter(day: string): void {
  handleDateChange(dateFilter.value === day ? "all" : day);
}

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
  return expandedIds.has(entry.id) || lines.length <= COLLAPSE_LINES
    ? lines.join("\n")
    : lines.slice(0, COLLAPSE_LINES).join("\n");
}

/** 将搜索关键词拆分高亮片段，避免 v-html XSS 风险 */
function highlightParts(text: string): Array<{ text: string; hit: boolean }> {
  const keyword = query.value.trim();
  if (!keyword) {
    return [{ text, hit: false }];
  }
  const lower = text.toLowerCase();
  const needle = keyword.toLowerCase();
  const parts: Array<{ text: string; hit: boolean }> = [];
  let cursor = 0;
  while (cursor < text.length) {
    const idx = lower.indexOf(needle, cursor);
    if (idx === -1) {
      parts.push({ text: text.slice(cursor), hit: false });
      break;
    }
    if (idx > cursor) {
      parts.push({ text: text.slice(cursor, idx), hit: false });
    }
    parts.push({ text: text.slice(idx, idx + keyword.length), hit: true });
    cursor = idx + keyword.length;
  }
  return parts;
}

function toggleExpanded(id: number): void {
  if (expandedIds.has(id)) {
    expandedIds.delete(id);
  } else {
    expandedIds.add(id);
  }
}

function formatEntryText(entry: LogEntry): string {
  return `[${dayKey(entry.createdAt)} ${timeLabel(entry.createdAt)}] [${entry.level.toUpperCase()}] ${entry.message.trim()}`;
}

async function copyLine(entry: LogEntry): Promise<void> {
  await copyText(formatEntryText(entry));
  copiedId.value = entry.id;
  copiedAll.value = false;
  if (copiedTimer !== null) {
    window.clearTimeout(copiedTimer);
  }
  copiedTimer = window.setTimeout(() => {
    copiedId.value = null;
    copiedTimer = null;
  }, 1600);
}

async function copyVisibleLogs(): Promise<void> {
  if (!visibleLogs.value.length) {
    return;
  }
  const payload = visibleLogs.value.map(formatEntryText).join("\n");
  await copyText(payload);
  copiedAll.value = true;
  copiedId.value = null;
  if (copiedTimer !== null) {
    window.clearTimeout(copiedTimer);
  }
  copiedTimer = window.setTimeout(() => {
    copiedAll.value = false;
    copiedTimer = null;
  }, 1800);
}

function exportVisibleLogs(): void {
  if (!visibleLogs.value.length) {
    return;
  }
  const content = visibleLogs.value.map(formatEntryText).join("\n") + "\n";
  const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-");
  link.href = url;
  link.download = `bgmcat-logs-${stamp}.log`;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}

function scrollToBottom(): void {
  if (outputRef.value) {
    outputRef.value.scrollTop = outputRef.value.scrollHeight;
  }
}

function jumpToBottom(): void {
  follow.value = true;
  void nextTick(scrollToBottom);
}

function handleScroll(): void {
  const element = outputRef.value;
  if (!element) {
    return;
  }
  const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 28;
  if (!atBottom && follow.value) {
    follow.value = false;
  }
}

function restartAutoRefresh(): void {
  if (autoRefreshTimer !== null) {
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

onBeforeUnmount(() => {
  if (autoRefreshTimer !== null) {
    window.clearInterval(autoRefreshTimer);
  }
  if (copiedTimer !== null) {
    window.clearTimeout(copiedTimer);
  }
});
</script>

<template>
  <section class="logs-page">
    <div class="log-console">
      <!-- 顶部控制台工具栏（NapCat 终端风格） -->
      <header class="console-header">
        <div class="console-header-left">
          <button
            type="button"
            class="live-pill"
            :class="{ active: follow }"
            :title="follow ? '自动拉取并跟随最新日志（点击暂停）' : '已暂停跟随（点击恢复）'"
            @click="follow = !follow"
          >
            <span class="live-dot" />
            <span>{{ follow ? "实时跟随" : "已暂停" }}</span>
          </button>

          <div class="level-tabs" role="group" aria-label="按级别筛选">
            <button
              v-for="level in LEVELS"
              :key="level.key"
              type="button"
              class="level-tab"
              :class="[`level-${level.key}`, { active: levelFilter === level.key }]"
              :aria-pressed="levelFilter === level.key"
              @click="levelFilter = level.key"
            >
              <span>{{ level.label }}</span>
              <span class="level-count">{{ levelCounts[level.key] }}</span>
            </button>
          </div>

          <AppSelect
            class="log-date-select"
            size="sm"
            :model-value="dateFilter"
            :options="dateOptions"
            aria-label="按日期筛选日志"
            @change="handleDateChange"
          />
        </div>

        <div class="console-header-right">
          <label class="console-search">
            <AppNavIcon name="search" class="console-search-icon" aria-hidden="true" />
            <input
              v-model="query"
              type="search"
              placeholder="过滤关键词…"
              aria-label="搜索日志内容"
            />
          </label>

          <div class="console-actions">
            <button
              type="button"
              class="console-btn"
              :class="{ active: wrapLines }"
              title="切换自动折行"
              @click="toggleWrap"
            >
              折行
            </button>

            <div class="font-stepper" role="group" aria-label="字号">
              <button
                type="button"
                class="console-btn stepper-btn"
                :disabled="logFontSize <= MIN_LOG_FONT_SIZE"
                title="减小字号"
                @click="setLogFontSize(logFontSize - 1)"
              >
                A-
              </button>
              <button
                type="button"
                class="console-btn stepper-btn"
                :disabled="logFontSize >= MAX_LOG_FONT_SIZE"
                title="增大字号"
                @click="setLogFontSize(logFontSize + 1)"
              >
                A+
              </button>
            </div>

            <button
              type="button"
              class="console-btn"
              :disabled="!visibleLogs.length"
              :title="copiedAll ? '已复制当前日志' : '复制当前显示的全部日志'"
              @click="copyVisibleLogs"
            >
              <AppNavIcon :name="copiedAll ? 'check' : 'copy'" class="btn-icon" aria-hidden="true" />
              <span class="hidden sm:inline">{{ copiedAll ? "已复制" : "复制" }}</span>
            </button>

            <button
              type="button"
              class="console-btn"
              :disabled="!visibleLogs.length"
              title="导出为 .log 文件"
              @click="exportVisibleLogs"
            >
              <AppNavIcon name="download" class="btn-icon" aria-hidden="true" />
              <span class="hidden sm:inline">导出</span>
            </button>

            <button
              type="button"
              class="console-btn icon-only"
              :disabled="isBusy('logs')"
              title="立即刷新"
              aria-label="立即刷新"
              @click="refreshLogs()"
            >
              <AppNavIcon name="refresh" class="btn-icon" :class="{ 'animate-spin': isBusy('logs') }" aria-hidden="true" />
            </button>
          </div>
        </div>
      </header>

      <!-- 终端正文区 -->
      <div class="console-body-wrap">
        <div
          ref="outputRef"
          class="console-body"
          :class="{ 'is-nowrap': !wrapLines }"
          :style="{ fontSize: `${logFontSize}px` }"
          @scroll.passive="handleScroll"
        >
          <div v-if="!visibleLogs.length" class="console-empty">
            <AppNavIcon name="logs" class="size-7 opacity-40" aria-hidden="true" />
            <p class="empty-title">{{ logs.length ? "没有匹配的日志记录" : "暂无运行日志" }}</p>
            <p class="empty-sub">
              {{ logs.length ? "可尝试清空搜索关键词、切换日期或日志级别。" : "订阅刷新、BT 下载、云盘归档等后台事件会实时输出在这里。" }}
            </p>
          </div>

          <template v-for="(entry, index) in visibleLogs" :key="entry.id">
            <div v-if="startsNewDay(index)" class="day-divider">
              <button
                type="button"
                class="day-divider-btn"
                :title="dateFilter === dayKey(entry.createdAt) ? '点击显示全部日期' : `只看 ${dayKey(entry.createdAt)} 的日志`"
                @click="toggleDayFilter(dayKey(entry.createdAt))"
              >
                <span>{{ dayKey(entry.createdAt) }}</span>
              </button>
            </div>

            <div class="term-row" :class="`is-${entry.level}`">
              <span class="term-lineno" aria-hidden="true">{{ index + 1 }}</span>
              <time class="term-time">{{ timeLabel(entry.createdAt) }}</time>
              <span class="term-level" :class="`lvl-${entry.level}`">{{ entry.level.toUpperCase() }}</span>
              <div class="term-content">
                <div class="term-msg">
                  <template v-for="(part, pIdx) in highlightParts(shownMessage(entry))" :key="pIdx">
                    <mark v-if="part.hit" class="log-mark">{{ part.text }}</mark>
                    <template v-else>{{ part.text }}</template>
                  </template>
                </div>
                <button
                  v-if="messageLines(entry).length > COLLAPSE_LINES"
                  type="button"
                  class="term-expand"
                  @click="toggleExpanded(entry.id)"
                >
                  {{ expandedIds.has(entry.id) ? "收起详情" : `展开其余 ${messageLines(entry).length - COLLAPSE_LINES} 行` }}
                </button>
              </div>
              <button
                type="button"
                class="term-copy"
                :class="{ copied: copiedId === entry.id }"
                :title="copiedId === entry.id ? '已复制' : '复制此行'"
                :aria-label="copiedId === entry.id ? '已复制' : '复制此行'"
                @click="copyLine(entry)"
              >
                <AppNavIcon :name="copiedId === entry.id ? 'check' : 'copy'" class="size-3.5" aria-hidden="true" />
              </button>
            </div>
          </template>
        </div>

        <!-- 向上翻阅旧日志时浮现「回到底部」按钮 -->
        <Transition name="fade-up">
          <button
            v-if="!follow && visibleLogs.length > 0"
            type="button"
            class="jump-bottom-btn"
            @click="jumpToBottom"
          >
            <AppNavIcon name="chevron-down" class="size-3.5" aria-hidden="true" />
            <span>回到底部</span>
          </button>
        </Transition>
      </div>

      <!-- 底部终端状态栏 -->
      <footer class="console-footer">
        <button
          type="button"
          class="footer-path"
          :title="logFilePath ? `${logFilePath}（点击复制路径）` : undefined"
          :disabled="!logFilePath"
          @click="copyText(logFilePath)"
        >
          {{ logFilePath || "运行时内存日志" }}
        </button>
        <div class="footer-meta">
          <span>{{ visibleLogs.length }} / {{ logs.length }} 条</span>
          <span class="footer-sep">·</span>
          <span>{{ logFontSize }}px</span>
          <span class="footer-sep hidden sm:inline">·</span>
          <span class="hidden sm:inline">{{ follow ? "每 3 秒自动同步" : "手动刷新模式" }}</span>
        </div>
      </footer>
    </div>
  </section>
</template>

<style scoped>
.logs-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  padding: 6px 0 4px;
}

.log-console {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink);
}

/* 顶部工具栏：与全站卡片头部风格统一 */
.console-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 12px;
  padding: 10px 12px;
  background: var(--tint-1);
  border-bottom: 1px solid var(--line);
}

.console-header-left,
.console-header-right {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.live-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 10px;
  border-radius: 999px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--muted);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}
.live-pill:hover {
  color: var(--ink);
  background: var(--panel);
}
.live-pill.active {
  border-color: color-mix(in srgb, var(--ok) 35%, var(--line));
  background: var(--ok-soft);
  color: var(--ok);
}
.live-dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: var(--warn);
}
.live-pill.active .live-dot {
  background: var(--ok);
  animation: pulse-dot 2s infinite;
}
@keyframes pulse-dot {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.45; }
}

.level-tabs {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: 9px;
  background: var(--panel);
  border: 1px solid var(--line);
}

.level-tab {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding: 0 8px;
  border-radius: 7px;
  border: 0;
  background: transparent;
  color: var(--muted);
  font-size: 12px;
  font-weight: 550;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.14s, color 0.14s;
}
.level-tab:hover {
  color: var(--ink);
  background: var(--tint-2);
}
.level-tab.active {
  background: var(--card);
  color: var(--ink);
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.08);
}
.level-tab.level-error.active {
  background: var(--err-soft);
  color: var(--err);
  box-shadow: none;
}
.level-tab.level-warn.active {
  background: var(--warn-soft);
  color: var(--warn);
  box-shadow: none;
}
.level-tab.level-info.active {
  background: var(--accent-soft);
  color: var(--accent-vivid);
  box-shadow: none;
}

.level-count {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  opacity: 0.78;
  font-variant-numeric: tabular-nums;
}

.log-date-select :deep(.app-select-trigger) {
  min-height: 28px;
  height: 28px;
  padding: 0 9px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 550;
}

.console-search {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 9px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--muted);
  min-width: 11rem;
  transition: border-color 0.15s;
}
.console-search:focus-within {
  border-color: var(--accent);
  color: var(--ink);
}
.console-search-icon {
  width: 13px;
  height: 13px;
  flex-shrink: 0;
}
.console-search input {
  width: 100%;
  min-width: 0;
  border: 0;
  background: transparent;
  color: var(--ink);
  font-size: 12px;
  outline: none;
}
.console-search input::placeholder {
  color: var(--muted);
}

.console-actions {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.console-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  height: 28px;
  padding: 0 9px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink-secondary);
  font-size: 12px;
  font-weight: 550;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.14s, border-color 0.14s, color 0.14s;
}
.console-btn:hover:not(:disabled) {
  background: var(--panel);
  color: var(--ink);
}
.console-btn.active {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent-vivid);
}
.console-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.console-btn.icon-only {
  width: 28px;
  padding: 0;
}
.btn-icon {
  width: 13.5px;
  height: 13.5px;
}

.font-stepper {
  display: inline-flex;
}
.stepper-btn {
  padding: 0 7px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
}
.stepper-btn:first-child {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
  border-right: 0;
}
.stepper-btn:last-child {
  border-top-left-radius: 0;
  border-bottom-left-radius: 0;
}

/* 日志正文区 */
.console-body-wrap {
  position: relative;
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.console-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  padding: 6px 0 14px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  line-height: 1.58;
  scrollbar-color: var(--line) transparent;
  scrollbar-width: thin;
}
.console-body::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}
.console-body::-webkit-scrollbar-thumb {
  background: var(--line);
  border-radius: 999px;
}
.console-body::-webkit-scrollbar-thumb:hover {
  background: var(--muted);
}

.console-empty {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 32px;
  color: var(--muted);
  font-family: inherit;
  text-align: center;
}
.empty-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--ink);
}
.empty-sub {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}

.day-divider {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 14px;
  background: color-mix(in srgb, var(--card) 92%, transparent);
  backdrop-filter: blur(6px);
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
}
.day-divider::after {
  content: "";
  flex: 1 1 auto;
  height: 1px;
  background: var(--line);
}
.day-divider-btn {
  border: 0;
  background: transparent;
  padding: 0;
  color: inherit;
  font: inherit;
  letter-spacing: inherit;
  cursor: pointer;
  transition: color 0.14s;
}
.day-divider-btn:hover {
  color: var(--ink);
}

.term-row {
  display: grid;
  grid-template-columns: 2.8rem 4.9rem 3.3rem minmax(0, 1fr) 26px;
  align-items: start;
  gap: 0 10px;
  padding: 3.5px 12px 3.5px 10px;
  border-left: 2px solid transparent;
  transition: background 0.1s;
}
.term-row:hover {
  background: var(--tint-1);
}
.term-row.is-warn {
  border-left-color: var(--warn);
  background: color-mix(in srgb, var(--warn-soft) 55%, transparent);
}
.term-row.is-warn:hover {
  background: var(--warn-soft);
}
.term-row.is-error {
  border-left-color: var(--err);
  background: color-mix(in srgb, var(--err-soft) 60%, transparent);
}
.term-row.is-error:hover {
  background: var(--err-soft);
}

.term-lineno {
  text-align: right;
  color: var(--muted);
  opacity: 0.55;
  font-variant-numeric: tabular-nums;
  user-select: none;
}

.term-time {
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  user-select: none;
}

.term-level {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 18px;
  margin-top: 1px;
  border-radius: 5px;
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.03em;
  user-select: none;
}
.lvl-info {
  background: var(--panel);
  color: var(--ink-secondary);
}
.lvl-warn {
  background: var(--warn-soft);
  color: var(--warn);
}
.lvl-error {
  background: var(--err-soft);
  color: var(--err);
}

.term-content {
  min-width: 0;
}

.term-msg {
  color: var(--ink);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.console-body.is-nowrap .term-msg {
  white-space: pre;
  overflow-wrap: normal;
}
.term-row.is-warn .term-msg {
  color: var(--ink);
}
.term-row.is-error .term-msg {
  color: var(--err);
}

.log-mark {
  background: var(--warn-soft);
  color: var(--ink);
  border-radius: 3px;
  padding: 0 2px;
}

.term-expand {
  margin-top: 2px;
  border: 0;
  background: transparent;
  padding: 0;
  color: var(--accent-vivid);
  font-size: 11px;
  font-weight: 600;
  cursor: pointer;
}
.term-expand:hover {
  text-decoration: underline;
}

.term-copy {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 22px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--muted);
  opacity: 0;
  cursor: pointer;
  transition: opacity 0.12s, background 0.12s, color 0.12s;
}
.term-row:hover .term-copy,
.term-copy:focus-visible,
.term-copy.copied {
  opacity: 1;
}
.term-copy:hover {
  background: var(--panel);
  color: var(--ink);
}
.term-copy.copied {
  color: var(--ok);
}

/* 回到底部悬浮按钮 */
.jump-bottom-btn {
  position: absolute;
  right: 16px;
  bottom: 14px;
  z-index: 3;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 30px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid var(--accent-line);
  background: var(--card);
  color: var(--accent-vivid);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  box-shadow: var(--strong-shadow);
  backdrop-filter: blur(8px);
  transition: background 0.15s, color 0.15s, border-color 0.15s, transform 0.15s;
}
.jump-bottom-btn:hover {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--accent-contrast);
  transform: translateY(-1px);
}

.fade-up-enter-active,
.fade-up-leave-active {
  transition: opacity 0.16s ease, transform 0.16s ease;
}
.fade-up-enter-from,
.fade-up-leave-to {
  opacity: 0;
  transform: translateY(6px);
}

/* 底部状态栏 */
.console-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  height: 30px;
  padding: 0 12px;
  background: var(--tint-1);
  border-top: 1px solid var(--line);
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
}

.footer-path {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  border: 0;
  background: transparent;
  padding: 0;
  color: inherit;
  cursor: pointer;
  text-align: left;
}
.footer-path:hover:not(:disabled) {
  color: var(--ink);
}
.footer-meta {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.footer-sep {
  opacity: 0.45;
}

@media (max-width: 640px) {
  .logs-page {
    padding: 4px 0 6px;
  }
  .console-search {
    flex: 1 1 100%;
    min-width: 0;
  }
  .term-row {
    grid-template-columns: auto auto minmax(0, 1fr) 24px;
    gap: 2px 8px;
    padding: 5px 10px;
  }
  .term-lineno {
    display: none;
  }
  .term-content {
    grid-column: 1 / -2;
  }
  .term-copy {
    opacity: 0.7;
  }
}
</style>
