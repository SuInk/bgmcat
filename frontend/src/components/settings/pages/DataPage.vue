<script setup lang="ts">
import { computed, inject, onMounted, ref, watch } from "vue";

import type { BackupSummary } from "@shared/index";

import {
  clearPlaybackCache,
  createBackup,
  deleteBackup,
  downloadBackup,
  getSystemStorage,
  importBackup,
  listBackups,
  openDataDir,
  restoreBackup,
  type SystemStorageUsage
} from "../../../api";
import AppDialog from "../../common/AppDialog.vue";
import AppNavIcon from "../../layout/AppNavIcon.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import StorageDonut, { type StorageDonutSegment } from "../StorageDonut.vue";
import { settingsPageVisibleKey } from "../context";
import { formatBytes } from "../../../lib/format";
import { selectTab } from "../../../stores/boot";
import { isBusy, withBusy } from "../../../stores/busy";
import { loadLocalDirectory, setFileSource } from "../../../stores/files";
import { toastError, updateFeedback } from "../../../stores/toasts";

const backups = ref<BackupSummary[]>([]);
const dataDir = ref("");
const storage = ref<SystemStorageUsage | null>(null);
const donutMode = ref<"disk" | "directory">("disk");
const backupImportInput = ref<HTMLInputElement | null>(null);
const pendingRestore = ref<{ name: string; label: string; file: File | null } | null>(null);

const loadingBackups = computed(() => isBusy("backups:list"));
const creatingBackup = computed(() => isBusy("backups:create"));
const restoringBackup = computed(() => isBusy("backups:restore"));
const clearingCache = computed(() => isBusy("playback-cache:clear"));

const isPageVisible = inject(settingsPageVisibleKey, () => true);

const BACKUP_REASON_LABELS: Record<string, string> = {
  manual: "手动",
  "pre-restore": "恢复前自动",
  unknown: "未知来源"
};

function backupReasonLabel(reason: string): string {
  if (reason.startsWith("pre-update-")) {
    return `更新前自动 · ${reason.slice("pre-update-".length)}`;
  }
  return BACKUP_REASON_LABELS[reason] ?? reason;
}

function formatBackupTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function backupFileName(path: string): string {
  return path.split(/[\\/]/).pop() ?? path;
}

const DIRECTORY_COLORS: Record<string, string> = {
  downloads: "var(--storage-downloads)",
  database: "var(--storage-database)",
  cache: "var(--storage-cache)",
  state: "var(--storage-state)",
  backups: "var(--storage-backups)"
};

const diskTotal = computed(() => {
  const s = storage.value;
  if (!s) {
    return 0;
  }
  return s.diskTotalBytes || s.appBytes || 0;
});

const diskSegments = computed<StorageDonutSegment[]>(() => {
  const s = storage.value;
  if (!s) {
    return [];
  }
  if (!s.diskTotalBytes) {
    return directorySegments.value;
  }
  const diskUsedBytes = Math.max(0, s.diskTotalBytes - s.diskFreeBytes);
  return [
    { key: "app", label: "Bangumi Cat 数据目录", bytes: s.appBytes, color: "var(--accent)" },
    {
      key: "system",
      label: "其它程序与系统",
      bytes: Math.max(0, diskUsedBytes - s.appBytes),
      color: "var(--storage-system)"
    },
    { key: "free", label: "可用空间", bytes: s.diskFreeBytes, color: "var(--storage-free)" }
  ];
});

const directorySegments = computed<StorageDonutSegment[]>(() => {
  const s = storage.value;
  if (!s?.directories?.length) {
    return [];
  }
  return s.directories.map((dir) => ({
    key: dir.key,
    label: dir.label,
    bytes: dir.bytes,
    color: DIRECTORY_COLORS[dir.key] ?? "var(--accent)"
  }));
});

const effectiveDonutMode = computed<"disk" | "directory">(() =>
  storage.value?.diskTotalBytes ? donutMode.value : "directory"
);

const activeDonutSegments = computed(() =>
  effectiveDonutMode.value === "disk" ? diskSegments.value : directorySegments.value
);

const activeDonutTotal = computed(() =>
  effectiveDonutMode.value === "disk" ? diskTotal.value : (storage.value?.appBytes ?? 0)
);

const activeDonutCenterValue = computed(() => {
  const s = storage.value;
  if (!s) {
    return "—";
  }
  if (effectiveDonutMode.value === "disk" && s.diskTotalBytes > 0) {
    return formatBytes(s.diskFreeBytes);
  }
  return formatBytes(s.appBytes);
});

const activeDonutCenterLabel = computed(() => {
  const s = storage.value;
  if (effectiveDonutMode.value === "disk" && s?.diskTotalBytes) {
    return `可用 / ${formatBytes(s.diskTotalBytes)}`;
  }
  return "数据目录";
});

function storageWidth(bytes: number, total: number): string {
  if (total <= 0 || bytes <= 0) {
    return "0%";
  }
  return `${Math.min(100, (bytes / total) * 100)}%`;
}

function storageShareLabel(bytes: number, total: number): string {
  if (total <= 0 || bytes < 0) {
    return "—";
  }
  const percent = (bytes / total) * 100;
  if (percent > 0 && percent < 0.1) {
    return "<0.1%";
  }
  return `${percent >= 10 ? percent.toFixed(0) : percent.toFixed(1)}%`;
}

function formatNumber(value: number): string {
  return value.toLocaleString();
}

function shortDirPath(path: string): string {
  if (!dataDir.value) {
    return path;
  }
  return path.replace(dataDir.value, "~" === dataDir.value ? "" : ".").replace(/^\.$/, ".");
}

async function refreshStorageAndBackups(): Promise<void> {
  await withBusy("backups:list", async () => {
    try {
      const [backupRes, storageRes] = await Promise.all([listBackups(), getSystemStorage()]);
      backups.value = backupRes.backups;
      dataDir.value = storageRes.dataDir || backupRes.dataDir;
      storage.value = storageRes;
    } catch (error) {
      toastError(error, "读取存储信息失败。");
    }
  });
}

function openLocalFiles(): void {
  setFileSource("local");
  void loadLocalDirectory("");
  selectTab("files");
}

async function runOpenDataDir(): Promise<void> {
  try {
    const result = await openDataDir();
    dataDir.value = result.path;
    updateFeedback(result.opened ? "已打开目录" : result.path, result.opened ? "success" : "info");
  } catch (error) {
    toastError(error, "打开目录失败。");
  }
}

async function runClearCache(): Promise<void> {
  await withBusy("playback-cache:clear", async () => {
    try {
      const result = await clearPlaybackCache();
      storage.value = await getSystemStorage();
      updateFeedback(
        result.removedCount
          ? `已清理缓存，释放 ${formatBytes(result.freedBytes)}`
          : "暂无可清理缓存",
        "success"
      );
    } catch (error) {
      toastError(error, "清理缓存失败。");
    }
  });
}

async function runCreateBackup(): Promise<void> {
  await withBusy("backups:create", async () => {
    try {
      const result = await createBackup("manual");
      backups.value = result.backups;
      storage.value = await getSystemStorage();
      updateFeedback(`已备份 ${result.name}（${formatBytes(result.size)}）`, "success");
    } catch (error) {
      toastError(error, "创建备份失败。");
    }
  });
}

async function runDownloadBackup(name: string): Promise<void> {
  await withBusy(`backups:download:${name}`, async () => {
    try {
      await downloadBackup(name);
    } catch (error) {
      toastError(error, "下载备份失败。");
    }
  });
}

async function runDeleteBackup(name: string): Promise<void> {
  await withBusy(`backups:delete:${name}`, async () => {
    try {
      backups.value = (await deleteBackup(name)).backups;
      storage.value = await getSystemStorage();
      updateFeedback("已删除备份", "success");
    } catch (error) {
      toastError(error, "删除备份失败。");
    }
  });
}

function askRestore(backup: BackupSummary): void {
  pendingRestore.value = {
    name: backup.name,
    label: `${formatBackupTime(backup.createdAt)} · ${backup.name}`,
    file: null
  };
}

function pickBackupFile(event: Event): void {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (file) {
    pendingRestore.value = { name: file.name, label: `导入文件 ${file.name}`, file };
  }
  input.value = "";
}

async function confirmRestore(): Promise<void> {
  const pending = pendingRestore.value;
  if (!pending) {
    return;
  }
  await withBusy("backups:restore", async () => {
    try {
      const result = pending.file ? await importBackup(pending.file) : await restoreBackup(pending.name);
      pendingRestore.value = null;
      await refreshStorageAndBackups();
      updateFeedback(
        `已恢复备份（原数据已另存为 ${backupFileName(result.preRestoreBackupPath)}）`,
        "success"
      );
    } catch (error) {
      toastError(error, "恢复备份失败。");
    }
  });
}

watch(
  () => isPageVisible("data"),
  (visible) => {
    if (visible) {
      void refreshStorageAndBackups();
    }
  }
);

onMounted(() => {
  void refreshStorageAndBackups();
});
</script>

<template>
  <SettingsSection id="storage" title="存储空间">
    <div class="settings-group">
      <SettingsItem as="div" stacked>
        <div class="grid w-full min-w-0 gap-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div v-if="storage?.diskTotalBytes" class="storage-mode-tabs" role="tablist" aria-label="饼图视角">
              <button
                type="button"
                role="tab"
                class="storage-mode-tab"
                :class="{ 'is-active': effectiveDonutMode === 'disk' }"
                :aria-selected="effectiveDonutMode === 'disk'"
                @click="donutMode = 'disk'"
              >
                整块磁盘
              </button>
              <button
                type="button"
                role="tab"
                class="storage-mode-tab"
                :class="{ 'is-active': effectiveDonutMode === 'directory' }"
                :aria-selected="effectiveDonutMode === 'directory'"
                @click="donutMode = 'directory'"
              >
                数据目录
              </button>
            </div>
            <span v-else class="text-sm font-semibold text-[var(--ink)]">数据目录占比</span>

            <div class="settings-actions">
              <button
                type="button"
                class="icon-btn"
                :disabled="loadingBackups"
                title="重新统计存储占用"
                aria-label="重新统计存储占用"
                @click="refreshStorageAndBackups()"
              >
                <AppNavIcon name="refresh" :class="{ 'animate-spin': loadingBackups }" aria-hidden="true" />
              </button>
              <button type="button" class="settings-secondary-button" @click="runOpenDataDir()">打开目录</button>
              <button
                type="button"
                class="settings-secondary-button"
                :disabled="clearingCache"
                @click="runClearCache()"
              >
                {{
                  clearingCache
                    ? "清理中…"
                    : storage && storage.cacheBytes > 0
                      ? `清理缓存 (${formatBytes(storage.cacheBytes)})`
                      : "清理缓存"
                }}
              </button>
            </div>
          </div>

          <div v-if="!storage" class="py-6 text-center text-xs text-[var(--muted)]">正在统计存储占用…</div>

          <template v-else>
            <div class="storage-overview">
              <StorageDonut
                :segments="activeDonutSegments"
                :total="activeDonutTotal"
                :center-value="activeDonutCenterValue"
                :center-label="activeDonutCenterLabel"
              />
              <ul class="storage-legend">
                <li v-for="segment in activeDonutSegments" :key="segment.key" class="storage-legend-row">
                  <span class="storage-legend-dot" :style="{ background: segment.color }" aria-hidden="true" />
                  <span class="storage-legend-label">{{ segment.label }}</span>
                  <span class="storage-legend-size">{{ formatBytes(segment.bytes) }}</span>
                  <span class="storage-legend-percent">{{ storageShareLabel(segment.bytes, activeDonutTotal) }}</span>
                </li>
              </ul>
            </div>

            <div class="storage-breakdown">
              <div class="storage-breakdown-head">
                <h3>数据目录里是什么</h3>
                <span class="text-xs text-[var(--muted)] tabular-nums">
                  {{ formatBytes(storage.appBytes) }} · {{ formatNumber(storage.appFiles) }} 个文件
                </span>
              </div>
              <div
                v-if="directorySegments.length > 0 && storage.appBytes > 0"
                class="storage-bar"
                role="img"
                aria-label="数据目录分类占比"
              >
                <span
                  v-for="segment in directorySegments.filter((item) => item.bytes > 0)"
                  :key="segment.key"
                  class="storage-bar-part"
                  :style="{ background: segment.color, width: storageWidth(segment.bytes, storage.appBytes) }"
                  :title="`${segment.label} ${formatBytes(segment.bytes)}（${storageShareLabel(segment.bytes, storage.appBytes)}）`"
                />
              </div>
              <ul class="storage-legend storage-legend-wide">
                <li v-for="segment in directorySegments" :key="segment.key" class="storage-legend-row">
                  <span class="storage-legend-dot" :style="{ background: segment.color }" aria-hidden="true" />
                  <span class="storage-legend-label">{{ segment.label }}</span>
                  <span class="storage-legend-size">{{ formatBytes(segment.bytes) }}</span>
                  <span class="storage-legend-percent">{{ storageShareLabel(segment.bytes, storage.appBytes) }}</span>
                </li>
              </ul>
            </div>

            <div v-if="storage.directories?.length" class="storage-breakdown">
              <div class="storage-breakdown-head">
                <h3>按目录</h3>
                <span class="text-xs text-[var(--muted)]">占比以数据目录为分母</span>
              </div>
              <div class="overflow-x-auto">
                <table class="storage-dir-table">
                  <thead>
                    <tr>
                      <th scope="col">目录</th>
                      <th scope="col" class="storage-dir-num">大小</th>
                      <th scope="col" class="storage-dir-num">占比</th>
                      <th scope="col" class="storage-dir-num">文件数</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="dir in storage.directories" :key="dir.key">
                      <td>
                        <div class="flex flex-wrap items-center gap-2">
                          <span class="storage-legend-dot" :style="{ background: DIRECTORY_COLORS[dir.key] ?? 'var(--accent)' }" aria-hidden="true" />
                          <span class="font-medium text-[var(--ink)]">{{ dir.label }}</span>
                          <code class="storage-dir-key font-mono text-[var(--muted)]">{{ shortDirPath(dir.path) }}</code>
                          <button
                            v-if="dir.key === 'downloads'"
                            type="button"
                            class="settings-secondary-button !min-h-[28px] !px-2.5 !text-[11px]"
                            @click="openLocalFiles()"
                          >
                            浏览与清理
                          </button>
                        </div>
                      </td>
                      <td class="storage-dir-num text-[var(--ink-secondary)]">{{ formatBytes(dir.bytes) }}</td>
                      <td class="storage-dir-num text-[var(--muted)]">{{ storageShareLabel(dir.bytes, storage.appBytes) }}</td>
                      <td class="storage-dir-num text-[var(--muted)]">{{ formatNumber(dir.files) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <p class="text-xs leading-5 text-[var(--muted)]">
              数据目录 <code class="rounded bg-[var(--panel)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--ink-secondary)]">{{ dataDir || "读取中…" }}</code>。
              缓存可在上方一键清理；已下载番剧可在「浏览与清理」中按文件查看或删除。
            </p>
          </template>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="data" title="数据备份">
    <div class="settings-group">
      <SettingsItem as="div" stacked>
        <div class="grid w-full min-w-0 gap-3">
          <div class="settings-actions">
            <button type="button" class="btn-primary px-3.5 py-1.5 text-xs font-semibold" :disabled="creatingBackup" @click="runCreateBackup()">
              {{ creatingBackup ? "备份中…" : "新建备份" }}
            </button>
            <button type="button" class="settings-secondary-button" :disabled="restoringBackup" @click="backupImportInput?.click()">
              导入恢复
            </button>
            <input ref="backupImportInput" type="file" accept=".zip,application/zip" class="hidden" @change="pickBackupFile" />
          </div>

          <p v-if="loadingBackups && !backups.length" class="settings-desc">读取中…</p>
          <p v-else-if="!backups.length" class="settings-desc">暂无备份</p>
          <div v-for="backup in backups" v-else :key="backup.name" class="settings-record">
            <span class="min-w-0 text-sm text-[var(--ink)]">
              <strong>{{ formatBackupTime(backup.createdAt) }}</strong>
              <span class="ml-2 text-xs text-[var(--muted)]">{{ formatBytes(backup.size) }}</span>
              <span class="settings-record-meta">{{ backupReasonLabel(backup.reason) }} · {{ backup.name }}</span>
            </span>
            <span class="settings-actions">
              <button type="button" class="settings-secondary-button" @click="runDownloadBackup(backup.name)">下载</button>
              <button type="button" class="settings-secondary-button" :disabled="restoringBackup" @click="askRestore(backup)">恢复</button>
              <button type="button" class="settings-secondary-button" @click="runDeleteBackup(backup.name)">删除</button>
            </span>
          </div>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <AppDialog v-if="pendingRestore" title="确认恢复备份？" :dismissible="!restoringBackup" @close="pendingRestore = null">
    <p class="text-sm leading-6 text-[var(--muted)]">
      将用 <strong class="text-[var(--ink)]">{{ pendingRestore.label }}</strong> 覆盖当前数据（恢复前会自动备份当前数据库）。
    </p>
    <template #footer>
      <button type="button" class="btn-ghost" :disabled="restoringBackup" @click="pendingRestore = null">取消</button>
      <button type="button" class="btn-primary" :disabled="restoringBackup" @click="confirmRestore()">
        {{ restoringBackup ? "恢复中…" : "确认恢复" }}
      </button>
    </template>
  </AppDialog>
</template>

<style scoped>
.storage-mode-tabs {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: 9px;
  border: 1px solid var(--line);
  background: var(--panel);
}

.storage-mode-tab {
  min-height: 30px;
  padding: 0 12px;
  border-radius: 7px;
  color: var(--ink-secondary);
  font-size: 12px;
  font-weight: 550;
  transition: background 0.15s, color 0.15s, box-shadow 0.15s;
}

.storage-mode-tab.is-active {
  background: var(--card);
  color: var(--ink);
  font-weight: 600;
  box-shadow: var(--soft-shadow);
}

.storage-overview {
  display: flex;
  align-items: center;
  gap: 24px;
  flex-wrap: wrap;
}

.storage-breakdown {
  padding-top: 14px;
  border-top: 1px solid var(--line);
  display: grid;
  gap: 12px;
}

.storage-breakdown-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}

.storage-breakdown-head h3 {
  margin: 0;
  color: var(--ink);
  font-size: 13.5px;
  font-weight: 600;
}

.storage-bar {
  display: flex;
  height: 12px;
  border-radius: 999px;
  overflow: hidden;
  background: var(--panel);
}

.storage-bar-part {
  min-width: 2px;
}

.storage-legend {
  list-style: none;
  margin: 0;
  padding: 0;
  flex: 1 1 240px;
  min-width: 220px;
  display: grid;
  gap: 8px;
  font-size: 13px;
}

.storage-legend-wide {
  flex: none;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  column-gap: 20px;
}

.storage-legend-row {
  display: grid;
  grid-template-columns: 10px 1fr auto auto;
  align-items: center;
  gap: 10px;
}

.storage-legend-dot {
  width: 10px;
  height: 10px;
  flex-shrink: 0;
  border-radius: 3px;
}

.storage-legend-label {
  overflow: hidden;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-legend-size {
  color: var(--ink-secondary);
  font-variant-numeric: tabular-nums;
}

.storage-legend-percent {
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  min-width: 42px;
  text-align: right;
}

.storage-dir-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.storage-dir-table th,
.storage-dir-table td {
  padding: 8px 6px;
  border-bottom: 1px solid var(--line);
  text-align: left;
  vertical-align: middle;
}

.storage-dir-table tbody tr:last-child td {
  border-bottom: none;
}

.storage-dir-table th {
  color: var(--muted);
  font-size: 11.5px;
  font-weight: 600;
}

.storage-dir-table .storage-dir-num {
  text-align: right;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.storage-dir-key {
  font-size: 11.5px;
}

@media (max-width: 640px) {
  .storage-dir-key {
    display: none;
  }

  .storage-dir-table th,
  .storage-dir-table td {
    padding-left: 4px;
    padding-right: 4px;
  }
}
</style>
