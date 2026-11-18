<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { sourceTypeLabel, type DownloadJob, type SourceType } from "@shared/index";
import { canPickDesktopDirectory, getCloudStatus, pickDesktopDirectory } from "../../api";
import { selectTab } from "../../stores/boot";
import { isBusy } from "../../stores/busy";
import {
  inferredManualSourceType,
  manualDownloadForm,
  parsedManualUrls,
  submitManualDownload
} from "../../stores/downloads";
import { cloudDriveConnected, settingsForm } from "../../stores/session";
import { updateFeedback } from "../../stores/toasts";
import AppDialog from "../common/AppDialog.vue";
import SegmentedControl from "../common/SegmentedControl.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";

const props = defineProps<{
  manualComposerOpen: boolean;
  deleteCandidate: DownloadJob | null;
  batchDeleteCandidates: DownloadJob[];
  deleteCandidateTitle: string;
}>();

const emit = defineEmits<{
  "update:manualComposerOpen": [open: boolean];
  closeDelete: [];
  confirmDelete: [deleteLocalFiles: boolean];
}>();

const manualOptionsOpen = ref(false);
const deleteLocalFiles = ref(true);

const activeCloudLabel = computed(() => {
  if (settingsForm.cloudProvider === "onedrive") return "OneDrive";
  if (settingsForm.cloudProvider === "webdav") return "WebDAV";
  return "Google Drive";
});

const saveToOptions = computed(() => [
  { value: "local" as const, label: "本机" },
  { value: "cloud" as const, label: activeCloudLabel.value }
]);

const cloudConnected = ref(false);
const canPickDirectory = canPickDesktopDirectory();
const canReadClipboard = typeof navigator !== "undefined" && Boolean(navigator.clipboard?.readText);

const effectiveManualSourceType = computed(
  () => manualDownloadForm.sourceTypeOverride ?? inferredManualSourceType.value
);

const manualUrlCount = computed(() => parsedManualUrls.value.length);

const manualSourceOptions: Array<{ value: SourceType | null; label: string; description: string }> = [
  { value: null, label: "自动识别", description: "按地址内容自动判断来源类型" },
  { value: "direct", label: "HTTP(S)", description: "普通直链下载地址" },
  { value: "magnet", label: "Magnet", description: "磁力链接" },
  { value: "torrent-url", label: "Torrent URL", description: ".torrent 文件地址" }
];

watch(
  () => props.manualComposerOpen,
  async (open) => {
    if (!open) {
      return;
    }
    manualDownloadForm.directory = "";
    manualDownloadForm.saveTo = "local";
    cloudConnected.value = cloudDriveConnected.value;
    try {
      const status = await getCloudStatus();
      cloudConnected.value = status.connected;
      cloudDriveConnected.value = status.connected;
    } catch {
      cloudConnected.value = false;
    }
    if (settingsForm.defaultSaveTo === "cloud" && cloudConnected.value) {
      manualDownloadForm.saveTo = "cloud";
    }
  }
);

watch(
  () => [props.deleteCandidate, props.batchDeleteCandidates.length] as const,
  ([candidate, batchCount]) => {
    if (candidate || batchCount > 0) {
      deleteLocalFiles.value = true;
    }
  }
);

watch(
  () => manualDownloadForm.url,
  (value) => {
    if (!value.trim()) {
      manualOptionsOpen.value = false;
    }
  }
);

async function pasteFromClipboard(): Promise<void> {
  try {
    const text = (await navigator.clipboard.readText()).trim();
    if (!text) {
      updateFeedback("剪贴板中没有可用文本。", "info");
      return;
    }
    manualDownloadForm.url = manualDownloadForm.url.trim()
      ? `${manualDownloadForm.url.trim()}\n${text}`
      : text;
  } catch {
    updateFeedback("无法直接读取剪贴板，请在输入框内按 Ctrl/Cmd + V 粘贴。", "info");
  }
}

async function pickManualDirectory(): Promise<void> {
  const picked = await pickDesktopDirectory();
  if (picked) {
    manualDownloadForm.directory = picked;
  }
}

async function submitManual(): Promise<void> {
  await submitManualDownload();
  emit("update:manualComposerOpen", false);
}

function closeManualComposer(clearUrl = false): void {
  if (clearUrl) {
    manualDownloadForm.url = "";
  }
  emit("update:manualComposerOpen", false);
}

function openCloudSettings(): void {
  emit("update:manualComposerOpen", false);
  selectTab("settings");
}
</script>

<template>
  <AppDialog
    v-if="manualComposerOpen"
    title="添加下载链接"
    @close="closeManualComposer()"
  >
    <div class="grid gap-3">
      <textarea
        v-model="manualDownloadForm.url"
        class="input-base min-h-32 w-full rounded-lg px-4 py-3 font-mono text-[13px] leading-5"
        placeholder="粘贴磁力、.torrent 或 HTTP(S) 链接（支持多行）"
      ></textarea>
      <div class="flex flex-wrap items-center gap-2.5">
        <button
          v-if="canReadClipboard"
          type="button"
          class="inline-flex items-center gap-1.5 rounded-full border border-[var(--line)] bg-[var(--tint-2)] px-3 py-1.5 text-xs font-semibold text-[var(--ink)] transition hover:border-[var(--accent)] hover:text-[var(--accent-vivid)]"
          @click="pasteFromClipboard"
        >
          <AppNavIcon name="copy" class="size-3.5" aria-hidden="true" />
          粘贴
        </button>
        <button
          type="button"
          class="rounded-full border border-[var(--line)] bg-[var(--tint-2)] px-3 py-1.5 text-xs font-semibold text-[var(--accent)] transition hover:border-[var(--accent)]"
          @click="manualOptionsOpen = true"
        >
          {{ sourceTypeLabel(effectiveManualSourceType) }}
        </button>
        <span
          v-if="manualUrlCount > 1"
          class="rounded-full border border-[var(--accent-line)] bg-[var(--accent-soft)] px-2.5 py-1 text-xs font-semibold tabular-nums text-[var(--accent-vivid)]"
        >
          {{ manualUrlCount }} 条链接
        </span>
      </div>

      <div class="grid gap-2 border-t border-[var(--line)] pt-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="text-sm font-semibold text-[var(--ink)]">保存到</span>
          <SegmentedControl v-model="manualDownloadForm.saveTo" :options="saveToOptions" aria-label="保存位置" />
        </div>
        <template v-if="manualDownloadForm.saveTo === 'local'">
          <div class="flex min-w-0 gap-2">
            <input
              v-model="manualDownloadForm.directory"
              class="input-base min-w-0 flex-1 rounded-lg px-3 py-2 text-sm"
              :placeholder="settingsForm.downloadDir || '默认下载目录'"
              aria-label="本机保存目录"
              spellcheck="false"
            />
            <button v-if="canPickDirectory" type="button" class="btn-ghost shrink-0 px-3 text-sm" @click="pickManualDirectory">选择…</button>
          </div>
        </template>
        <template v-else-if="!cloudConnected">
          <p class="text-xs leading-5 text-[var(--warn)]">
            未连接 {{ activeCloudLabel }} ·
            <button type="button" class="font-semibold underline" @click="openCloudSettings">前往连接</button>
          </p>
        </template>
      </div>
    </div>
    <template #footer>
      <button type="button" class="btn-ghost" @click="closeManualComposer(true)">取消</button>
      <button
        type="button"
        class="btn-primary"
        :disabled="isBusy('download:manual') || !manualUrlCount || (manualDownloadForm.saveTo === 'cloud' && !cloudConnected)"
        @click="submitManual"
      >
        {{
          isBusy("download:manual")
            ? "添加中…"
            : manualUrlCount > 1
              ? `添加 (${manualUrlCount})`
              : "添加"
        }}
      </button>
    </template>
  </AppDialog>

  <AppDialog
    v-if="manualOptionsOpen"
    title="来源类型"
    @close="manualOptionsOpen = false"
  >
    <div class="grid gap-2">
      <button
        v-for="option in manualSourceOptions"
        :key="option.label"
        type="button"
        class="flex items-center justify-between gap-3 rounded-lg border px-4 py-2.5 text-left transition"
        :class="manualDownloadForm.sourceTypeOverride === option.value ? 'border-[var(--accent)] bg-[var(--tint-2)]' : 'border-[var(--line)] bg-[var(--tint-2)] hover:border-[var(--accent)]'"
        @click="manualDownloadForm.sourceTypeOverride = option.value"
      >
        <span class="text-sm font-semibold text-[var(--ink)]">{{ option.label }}</span>
        <span v-if="manualDownloadForm.sourceTypeOverride === option.value" class="rounded-full bg-[var(--accent)] px-2 py-0.5 text-[11px] font-semibold text-[var(--accent-contrast)]">
          当前
        </span>
      </button>
    </div>
  </AppDialog>

  <AppDialog
    v-if="deleteCandidate || batchDeleteCandidates.length"
    :title="batchDeleteCandidates.length ? `删除 ${batchDeleteCandidates.length} 个任务` : '删除任务'"
    :description="deleteCandidate ? deleteCandidateTitle : undefined"
    @close="emit('closeDelete')"
  >
    <label class="flex cursor-pointer items-center gap-3 rounded-lg border border-[var(--line)] bg-[var(--tint-2)] px-4 py-3 text-sm font-semibold text-[var(--ink)]">
      <input v-model="deleteLocalFiles" type="checkbox" class="size-4 accent-[var(--accent)]" />
      <span>同时删除本地文件</span>
    </label>
    <template #footer>
      <button type="button" class="btn-ghost" @click="emit('closeDelete')">取消</button>
      <button type="button" class="btn-danger" @click="emit('confirmDelete', deleteLocalFiles)">
        删除
      </button>
    </template>
  </AppDialog>
</template>
