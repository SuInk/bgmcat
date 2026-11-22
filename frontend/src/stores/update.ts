import { computed, ref } from "vue";

import type { UpdatePolicy, UpdateReleaseSummary, UpdateStatus, UpdateVersionInfo } from "@shared/index";

import {
  checkForUpdate,
  downloadUpdate,
  getUpdateChangelog,
  getUpdatePolicy,
  getUpdateStatus,
  getUpdateVersion,
  installBackgroundService,
  installUpdate,
  rollbackUpdate,
  saveUpdatePolicy
} from "../api";
import { isBusy, withBusy } from "./busy";
import { markUpdateInstalling } from "../lib/backendState";
import { refreshSettings } from "./session";
import { toastError, updateFeedback } from "./toasts";

export const updateStatus = ref<UpdateStatus | null>(null);
export const updateVersion = ref<UpdateVersionInfo | null>(null);
export const updatePolicy = ref<UpdatePolicy>({
  autoDownload: true,
  autoInstall: false,
  mirror: "auto",
  channel: "stable"
});
export const updateReleases = ref<UpdateReleaseSummary[]>([]);

export const updateBusy = computed(() => isBusy("update-action"));

/** 有可用更新时侧栏版本号旁挂一个小黄点。发现新版本只提示，装不装由用户定。 */
export const updateAvailable = computed(
  () => Boolean(updateStatus.value?.updateAvailable) || updateStatus.value?.kind === "downloaded"
);

/**
 * 状态徽章。这五个态必须分开写，尤其是「升不了级」不能落进「已是最新」：
 * 两者都没有可点的按钮，含义却相反。
 */
export const updateStatusLabel = computed(() => {
  const status = updateStatus.value;
  if (!status) {
    return "尚未检查";
  }
  switch (status.kind) {
    case "unsupported":
      return "不支持自更新";
    case "blocked":
      return "暂时无法更新";
    case "no_release":
      return "暂无发布版本";
    case "downloading":
      return "正在下载并校验";
    case "downloaded":
      return "已下载，待安装";
    case "installing":
      return "正在安装";
    case "restart_required":
      return "等待重启";
    case "available":
      return "发现新版本";
    case "error":
      return "上次操作失败";
    case "up_to_date":
      return "已是最新";
    default:
      return "尚未检查";
  }
});

export const updateStatusTone = computed(() => {
  switch (updateStatus.value?.kind) {
    case "unsupported":
    case "blocked":
    case "error":
      return "err";
    case "no_release":
      return "muted";
    case "downloading":
    case "installing":
    case "downloaded":
    case "restart_required":
      return "warn";
    case "available":
      return "accent";
    case "up_to_date":
      return "ok";
    default:
      return "muted";
  }
});

/**
 * 检查完给一句人话。这里不能一律说「已经是最新版本」——「没检查出可升的东西」有好
 * 几种完全不同的原因（发布仓是空的、工作区脏、没有 origin），把它们说成「已是最新」
 * 会让人以为一切正常。
 */
function describeCheckResult(status: UpdateStatus): string {
  if (status.kind === "error") return status.lastError || "检查更新失败。";
  if (status.kind === "unsupported" || status.kind === "blocked") {
    return status.unsupportedReason || "当前无法更新。";
  }
  if (status.kind === "no_release") {
    return "发布仓里还没有任何版本。";
  }
  if (status.updateAvailable) {
    const source = status.source;
    if (source && source.behind > 0) {
      return `${source.upstream} 领先 ${source.behind} 个提交。`;
    }
    return `发现新版本 ${status.latestVersion}。`;
  }
  return status.source ? "已与远端同步。" : "已经是最新版本。";
}

let pollTimer: number | null = null;

/** 下载/安装期间把轮询加密，其余时候不轮询——状态页不该常驻打 API。 */
function syncPolling(): void {
  const busyKind = updateStatus.value?.kind;
  const shouldPoll = busyKind === "downloading" || busyKind === "installing";
  if (shouldPoll && pollTimer === null) {
    pollTimer = window.setInterval(() => void refreshUpdateStatus(), 1000);
  } else if (!shouldPoll && pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
}

export function stopUpdatePolling(): void {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
}

export async function refreshUpdateStatus(): Promise<void> {
  try {
    updateStatus.value = await getUpdateStatus();
    syncPolling();
  } catch {
    // 状态查询会被轮询反复调用，失败只是这一轮没拿到，不值得弹 toast。
    stopUpdatePolling();
  }
}

export async function refreshUpdateOverview(): Promise<void> {
  const [version, status, policy, changelog] = await Promise.allSettled([
    getUpdateVersion(),
    getUpdateStatus(),
    getUpdatePolicy(),
    getUpdateChangelog(5)
  ]);
  if (version.status === "fulfilled") {
    updateVersion.value = version.value;
  }
  if (status.status === "fulfilled") {
    updateStatus.value = status.value;
    syncPolling();
  }
  if (policy.status === "fulfilled") {
    updatePolicy.value = policy.value;
  }
  if (changelog.status === "fulfilled") {
    updateReleases.value = changelog.value;
  }
}

/** 切换更新通道后立刻重新检查，让「最新版本」跟着通道变。 */
export async function setUpdateChannel(channel: string): Promise<void> {
  if (channel === updatePolicy.value.channel) return;
  const previous = updatePolicy.value.channel;
  updatePolicy.value = { ...updatePolicy.value, channel };
  try {
    updatePolicy.value = await saveUpdatePolicy({ channel });
  } catch (error) {
    updatePolicy.value = { ...updatePolicy.value, channel: previous };
    toastError(error, "切换更新通道失败。");
    return;
  }
  await runUpdateCheck();
}

export async function runUpdateCheck(): Promise<void> {
  await withBusy("update-action", async () => {
    try {
      updateStatus.value = await checkForUpdate();
      updateReleases.value = await getUpdateChangelog(5);
      updateFeedback(describeCheckResult(updateStatus.value), updateStatus.value.kind === "error" ? "error" : "success");
    } catch (error) {
      toastError(error, "检查更新失败。");
    }
  });
}

export async function runUpdateDownload(version?: string): Promise<void> {
  await withBusy("update-action", async () => {
    try {
      // 先把状态切到下载中，轮询才会立刻开始推进度条。
      void refreshUpdateStatus();
      updateStatus.value = await downloadUpdate(version);
      updateFeedback("更新包已下载并通过 SHA-256 校验。", "success");
    } catch (error) {
      toastError(error, "下载更新失败。");
      await refreshUpdateStatus();
    }
  });
}

export async function runUpdateInstall(version?: string): Promise<void> {
  await withBusy("update-action", async () => {
    try {
      markUpdateInstalling();
      updateStatus.value = await installUpdate(version);
      updateFeedback("正在备份并安装，应用会自动重启。", "success");
    } catch (error) {
      toastError(error, "安装更新失败。");
      await refreshUpdateStatus();
    }
  });
}

export async function runUpdateRollback(version: string): Promise<void> {
  await withBusy("update-action", async () => {
    try {
      markUpdateInstalling();
      updateStatus.value = await rollbackUpdate(version);
      updateFeedback(`正在回退到 ${version}。`, "success");
    } catch (error) {
      toastError(error, "回退失败。");
      await refreshUpdateStatus();
    }
  });
}

/**
 * 开自动安装会一并开自动下载：没下载过的东西装不了，让用户面对一个
 * 「开了却不生效」的开关是更糟的选择。这条规则服务端也有一份，前端这份只为了
 * 让界面立刻反映出联动，不是唯一防线。
 */
export async function persistUpdatePolicy(patch: Partial<UpdatePolicy>): Promise<void> {
  const next = { ...updatePolicy.value, ...patch };
  if (next.autoInstall) {
    next.autoDownload = true;
  }
  updatePolicy.value = next;
  try {
    updatePolicy.value = await saveUpdatePolicy(next);
    // 策略就是设置里的四个字段，保存完要把设置表单同步回来，否则它会显示成「有未保存改动」。
    await refreshSettings();
  } catch (error) {
    toastError(error, "保存更新策略失败。");
    await refreshUpdateOverview();
  }
}

export async function runInstallBackgroundService(): Promise<void> {
  await withBusy("update-action", async () => {
    try {
      const result = await installBackgroundService();
      updateFeedback(`已生成 ${result.manager} 单元：${result.path}`, "success");
      await refreshUpdateStatus();
    } catch (error) {
      toastError(error, "安装为后台服务失败。");
    }
  });
}
