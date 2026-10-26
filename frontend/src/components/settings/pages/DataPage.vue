<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import type { BackupSummary } from "@shared/index";

import { createBackup, deleteBackup, downloadBackup, importBackup, listBackups, openDataDir, restoreBackup } from "../../../api";
import AppDialog from "../../common/AppDialog.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { formatBytes } from "../../../lib/format";
import { isBusy, withBusy } from "../../../stores/busy";
import { toastError, updateFeedback } from "../../../stores/toasts";

const backups = ref<BackupSummary[]>([]);
const dataDir = ref("");
const backupImportInput = ref<HTMLInputElement | null>(null);
const pendingRestore = ref<{ name: string; label: string; file: File | null } | null>(null);

const loadingBackups = computed(() => isBusy("backups:list"));
const creatingBackup = computed(() => isBusy("backups:create"));
const restoringBackup = computed(() => isBusy("backups:restore"));

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

async function refreshBackups(): Promise<void> {
  await withBusy("backups:list", async () => {
    try {
      const result = await listBackups();
      backups.value = result.backups;
      dataDir.value = result.dataDir;
    } catch (error) {
      toastError(error, "读取备份列表失败。");
    }
  });
}

async function runOpenDataDir(): Promise<void> {
  try {
    const result = await openDataDir();
    dataDir.value = result.path;
    updateFeedback(result.opened ? "已打开数据目录。" : `数据目录：${result.path}`, result.opened ? "success" : "info");
  } catch (error) {
    toastError(error, "打开数据目录失败。");
  }
}

async function runCreateBackup(): Promise<void> {
  await withBusy("backups:create", async () => {
    try {
      const result = await createBackup("manual");
      backups.value = result.backups;
      updateFeedback(`已备份到 ${result.name}（${formatBytes(result.size)}）。`, "success");
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
      updateFeedback("已删除该备份。", "success");
    } catch (error) {
      toastError(error, "删除备份失败。");
    }
  });
}

// 恢复会整体替换数据库，必须二次确认——弹层里把代价说清楚。
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
      await refreshBackups();
      updateFeedback(
        `已从 ${result.restoredFrom} 恢复，恢复前的数据已另存为 ${backupFileName(result.preRestoreBackupPath)}。` +
          "请刷新页面；如果备份里的 token 与现在不同，还需要重新登录。",
        "success"
      );
    } catch (error) {
      toastError(error, "恢复备份失败。");
    }
  });
}

onMounted(() => {
  void refreshBackups();
});
</script>

<template>
  <SettingsSection id="data" title="备份与恢复">
    <div class="settings-group">
      <SettingsItem
        as="div"
        stacked
        title="数据目录与备份"
        desc="订阅、下载记录和设置都在数据目录的 app.db 里。备份是一份一致性快照，可下载留存，也可随时恢复。"
      >
        <div class="grid w-full min-w-0 gap-3">
          <div class="settings-actions">
            <code class="min-w-0 flex-1 break-all rounded-lg border border-[var(--line)] px-3 py-2 text-xs text-[var(--muted)]">
              {{ dataDir || "读取中…" }}
            </code>
            <button type="button" class="settings-secondary-button" @click="runOpenDataDir()">打开目录</button>
            <button type="button" class="settings-secondary-button" :disabled="creatingBackup" @click="runCreateBackup()">
              {{ creatingBackup ? "备份中…" : "立即备份" }}
            </button>
            <button type="button" class="settings-secondary-button" :disabled="restoringBackup" @click="backupImportInput?.click()">
              导入备份文件
            </button>
            <input ref="backupImportInput" type="file" accept=".zip,application/zip" class="hidden" @change="pickBackupFile" />
          </div>

          <p v-if="loadingBackups" class="settings-desc">读取中…</p>
          <p v-else-if="!backups.length" class="settings-desc">还没有备份。</p>
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
    <div class="grid gap-2 text-sm leading-6 text-[var(--muted)]">
      <p>
        将用 <strong class="text-[var(--ink)]">{{ pendingRestore.label }}</strong>
        整体替换当前数据库：备份之后新增的订阅、下载记录和设置都会消失。
      </p>
      <p>
        恢复前会自动把当前数据另存为一份 <code>pre-restore</code> 备份，随时可以退回来。
        恢复完成后请刷新页面；如果备份里的 token 与现在不同，还需要重新登录。
      </p>
    </div>
    <template #footer>
      <button type="button" class="btn-ghost" :disabled="restoringBackup" @click="pendingRestore = null">取消</button>
      <button type="button" class="btn-primary" :disabled="restoringBackup" @click="confirmRestore()">
        {{ restoringBackup ? "恢复中…" : "确认恢复" }}
      </button>
    </template>
  </AppDialog>
</template>
