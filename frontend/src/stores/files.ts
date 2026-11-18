import { computed, ref } from "vue";
import type { DownloadPlaybackInfo } from "@shared/index";
import {
  deleteCloudFile,
  deleteLocalFile,
  emptyLocalTrash,
  getDownloadPlaybackInfo,
  listCloudFiles,
  listLocalFiles,
  mkdirCloudFile,
  mkdirLocalFile,
  restoreLocalTrashFile,
  type CloudDriveQuota,
  type FileBrowserEntry
} from "../api";
import { withBusy } from "./busy";
import { toastError, updateFeedback } from "./toasts";

export type FileStorageSource = "local" | "cloud";

const SOURCE_STORAGE_KEY = "bgmcat.files.source";

function readInitialSource(): FileStorageSource {
  try {
    return window.localStorage.getItem(SOURCE_STORAGE_KEY) === "cloud" ? "cloud" : "local";
  } catch {
    return "local";
  }
}

export const fileSource = ref<FileStorageSource>(readInitialSource());

export function setFileSource(next: FileStorageSource): void {
  fileSource.value = next;
  try {
    window.localStorage.setItem(SOURCE_STORAGE_KEY, next);
  } catch {
    // 忽略无痕窗口异常
  }
}

/* ---------- 本地存储状态 ---------- */
export const localPath = ref("");
export const localRootPath = ref("");
export const localFreeBytes = ref(0);
export const localTotalBytes = ref(0);
export const localTrashCount = ref(0);
export const localTrashBytes = ref(0);
export const localEntries = ref<FileBrowserEntry[]>([]);
export const localLoading = ref(false);
export const localLoaded = ref(false);
export const localError = ref("");

/* ---------- 云盘状态 ---------- */
export const cloudProviderName = ref("Google Drive");
export const cloudConnected = ref<boolean | null>(null);
export const cloudConfigured = ref(false);
export const cloudArchiveFolderId = ref("root");
export const cloudTrail = ref<Array<{ id: string; name: string }>>([]);
export const cloudQuota = ref<CloudDriveQuota | null>(null);
export const cloudEntries = ref<FileBrowserEntry[]>([]);
export const cloudNextPageToken = ref("");
export const cloudLoading = ref(false);
export const cloudLoadingMore = ref(false);
export const cloudLoaded = ref(false);
export const cloudError = ref("");

export const cloudCurrentFolder = computed(
  () => cloudTrail.value[cloudTrail.value.length - 1] ?? { id: "default", name: "云盘" }
);

/* ---------- 内置流式播放状态 ---------- */
export interface ActiveFilePlaybackState {
  info: DownloadPlaybackInfo;
  entry: FileBrowserEntry;
  source: FileStorageSource;
  folderLabel: string;
  siblings: FileBrowserEntry[];
}

export const activeFilePlayback = ref<ActiveFilePlaybackState | null>(null);

export function closeFilePlayback(): void {
  activeFilePlayback.value = null;
}

export async function loadLocalDirectory(nextPath = localPath.value): Promise<void> {
  localLoading.value = true;
  localError.value = "";
  try {
    const res = await listLocalFiles(nextPath);
    localRootPath.value = res.rootPath;
    localPath.value = res.path;
    localFreeBytes.value = res.freeBytes;
    localTotalBytes.value = res.totalBytes;
    localTrashCount.value = res.trashCount ?? 0;
    localTrashBytes.value = res.trashBytes ?? 0;
    localEntries.value = res.entries;
    localLoaded.value = true;
  } catch (err) {
    localError.value = err instanceof Error ? err.message : String(err);
  } finally {
    localLoading.value = false;
  }
}

export async function loadCloudDirectory(
  options: { parent?: string; parentName?: string; resetTrail?: boolean } = {}
): Promise<void> {
  cloudLoading.value = true;
  cloudError.value = "";
  const targetParent = options.parent ?? (cloudTrail.value.length ? cloudCurrentFolder.value.id : "default");
  try {
    const res = await listCloudFiles({ parent: targetParent });
    if (res.providerName) {
      cloudProviderName.value = res.providerName;
    }
    cloudConnected.value = res.connected;
    cloudConfigured.value = res.configured;
    cloudArchiveFolderId.value = res.archiveFolderId || "root";
    cloudNextPageToken.value = res.nextPageToken ?? "";
    cloudQuota.value = res.quota ?? null;
    cloudEntries.value = res.entries;
    cloudLoaded.value = true;

    if (!res.connected) {
      cloudTrail.value = [{ id: "root", name: res.parentName || "云盘" }];
      return;
    }

    const resolvedName =
      options.parentName ||
      res.parentName ||
      (res.parent === "root" ? "我的云盘" : res.parent === res.archiveFolderId ? "归档目录" : "文件夹");

    if (options.resetTrail || cloudTrail.value.length === 0 || targetParent === "default") {
      cloudTrail.value = [{ id: res.parent, name: resolvedName }];
    } else {
      const existingIdx = cloudTrail.value.findIndex((item) => item.id === res.parent);
      if (existingIdx >= 0) {
        cloudTrail.value = cloudTrail.value.slice(0, existingIdx + 1);
      } else {
        cloudTrail.value = [...cloudTrail.value, { id: res.parent, name: resolvedName }];
      }
    }
  } catch (err) {
    cloudError.value = err instanceof Error ? err.message : String(err);
  } finally {
    cloudLoading.value = false;
  }
}

export async function loadMoreCloudFiles(): Promise<void> {
  if (!cloudNextPageToken.value || cloudLoadingMore.value || !cloudConnected.value) {
    return;
  }
  cloudLoadingMore.value = true;
  try {
    const res = await listCloudFiles({
      parent: cloudCurrentFolder.value.id,
      pageToken: cloudNextPageToken.value
    });
    cloudNextPageToken.value = res.nextPageToken ?? "";
    const seen = new Set(cloudEntries.value.map((item) => item.id));
    const appended = res.entries.filter((item) => !seen.has(item.id));
    cloudEntries.value = [...cloudEntries.value, ...appended];
  } catch (err) {
    toastError(err, "加载更多云盘文件失败");
  } finally {
    cloudLoadingMore.value = false;
  }
}

export async function refreshActiveFiles(): Promise<void> {
  if (fileSource.value === "cloud") {
    await loadCloudDirectory();
  } else {
    await loadLocalDirectory();
  }
}

export async function createSubfolderInCurrent(name: string): Promise<boolean> {
  const trimmed = name.trim();
  if (!trimmed) {
    return false;
  }
  return withBusy("files-mkdir", async () => {
    try {
      if (fileSource.value === "local") {
        await mkdirLocalFile(localPath.value, trimmed);
        await loadLocalDirectory(localPath.value);
      } else {
        await mkdirCloudFile(cloudCurrentFolder.value.id, trimmed);
        await loadCloudDirectory({ parent: cloudCurrentFolder.value.id });
      }
      updateFeedback(`已创建文件夹「${trimmed}」`, "success");
      return true;
    } catch (err) {
      toastError(err, "新建文件夹失败");
      return false;
    }
  });
}

export async function removeFileBrowserEntry(
  entry: FileBrowserEntry,
  options: { permanent?: boolean } = {}
): Promise<boolean> {
  return withBusy(`files-del-${entry.id}`, async () => {
    try {
      if (fileSource.value === "local") {
        const res = await deleteLocalFile(entry.path, Boolean(options.permanent));
        if (activeFilePlayback.value?.entry.id === entry.id) {
          closeFilePlayback();
        }
        await loadLocalDirectory(localPath.value);
        if (localError.value && localPath.value.startsWith(".trash")) {
          await loadLocalDirectory(".trash");
        }
        updateFeedback(
          res.trashed ? `已移入回收站：「${entry.name}」` : `已永久删除：「${entry.name}」`,
          "success"
        );
      } else {
        const res = await deleteCloudFile(entry.id);
        if (activeFilePlayback.value?.entry.id === entry.id) {
          closeFilePlayback();
        }
        cloudEntries.value = cloudEntries.value.filter((item) => item.id !== entry.id);
        updateFeedback(res.trashed ? `已移入回收站：「${entry.name}」` : `已删除：「${entry.name}」`, "success");
      }
      return true;
    } catch (err) {
      toastError(err, "删除失败");
      return false;
    }
  });
}

export async function restoreLocalTrashEntry(entry: FileBrowserEntry): Promise<boolean> {
  return withBusy(`files-restore-${entry.id}`, async () => {
    try {
      const res = await restoreLocalTrashFile(entry.path);
      await loadLocalDirectory(localPath.value);
      updateFeedback(`已还原至「${res.path}」`, "success");
      return true;
    } catch (err) {
      toastError(err, "还原失败");
      return false;
    }
  });
}

export async function emptyLocalTrashEntries(): Promise<boolean> {
  return withBusy("files-empty-trash", async () => {
    try {
      const res = await emptyLocalTrash();
      const nextPath = localPath.value.startsWith(".trash") ? ".trash" : localPath.value;
      await loadLocalDirectory(nextPath);
      updateFeedback(
        res.deletedCount > 0 ? `已清空回收站（共 ${res.deletedCount} 项）` : "回收站已空",
        "success"
      );
      return true;
    } catch (err) {
      toastError(err, "清空回收站失败");
      return false;
    }
  });
}

export async function openFilePlayback(
  entry: FileBrowserEntry,
  context?: { source?: FileStorageSource; entries?: FileBrowserEntry[]; folderLabel?: string }
): Promise<void> {
  if (!entry.playable || !entry.mediaId) {
    return;
  }
  const src = context?.source ?? fileSource.value;
  const list = context?.entries ?? (src === "local" ? localEntries.value : cloudEntries.value);
  const siblings = list.filter((item) => item.playable && Boolean(item.mediaId));
  const folderLabel =
    context?.folderLabel ??
    (src === "local"
      ? localPath.value
        ? `本地目录 / ${localPath.value}`
        : "本地下载目录"
      : `${cloudProviderName.value} / ${cloudTrail.value.map((c) => c.name).join(" / ")}`);

  await withBusy(`files-play-${entry.id}`, async () => {
    try {
      const info = await getDownloadPlaybackInfo(entry.mediaId);
      activeFilePlayback.value = {
        info,
        entry,
        source: src,
        folderLabel,
        siblings
      };
    } catch (err) {
      toastError(err, "无法启动播放");
    }
  });
}
