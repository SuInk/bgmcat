import { computed, ref } from "vue";

import type { DiscoveredTunnel, TunnelLogEntry, TunnelPreflightIssue, TunnelStatusSnapshot } from "@shared/index";

import {
  getTunnelPreflight,
  getTunnelStatus,
  listDiscoveredTunnels,
  listTunnelLogs,
  restartTunnel,
  startTunnel,
  stopTunnel,
  updateSettings
} from "../api";
import { isBusy, withBusy } from "./busy";
import { refreshSettings, settings, settingsForm } from "./session";
import { toastError, updateFeedback } from "./toasts";

export const tunnelStatus = ref<TunnelStatusSnapshot | null>(null);
export const tunnelPreflight = ref<TunnelPreflightIssue[]>([]);
export const tunnelLogs = ref<TunnelLogEntry[]>([]);
export const discoveredTunnels = ref<DiscoveredTunnel[]>([]);

export const tunnelBusy = computed(() => isBusy("tunnel-action"));
export const tunnelBlockers = computed(() => tunnelPreflight.value.filter((issue) => issue.level === "block"));
export const tunnelWarnings = computed(() => tunnelPreflight.value.filter((issue) => issue.level === "warn"));

const STATE_LABELS: Record<TunnelStatusSnapshot["state"], string> = {
  stopped: "已停止",
  starting: "启动中",
  online: "已连通",
  degraded: "连接不稳定",
  error: "异常"
};

export function tunnelStateLabel(state: TunnelStatusSnapshot["state"] | undefined): string {
  return state ? STATE_LABELS[state] : "-";
}

export async function refreshTunnel(): Promise<void> {
  const [status, preflight] = await Promise.allSettled([getTunnelStatus(), getTunnelPreflight()]);
  if (status.status === "fulfilled") {
    tunnelStatus.value = status.value;
  }
  if (preflight.status === "fulfilled") {
    tunnelPreflight.value = preflight.value;
  }
}

export async function refreshTunnelLogs(limit = 100): Promise<void> {
  tunnelLogs.value = await listTunnelLogs(limit).catch(() => []);
}

export async function refreshDiscoveredTunnels(): Promise<void> {
  discoveredTunnels.value = await listDiscoveredTunnels().catch(() => []);
}

/**
 * 开关是立即动作而不是表单字段：先把 tunnelEnabled 回写后端（保证重启后一致），
 * 再显式调用 start/stop，避免用户开了开关却忘记保存。
 */
export async function setTunnelEnabled(enabled: boolean): Promise<void> {
  await withBusy("tunnel-action", async () => {
    try {
      settingsForm.tunnelEnabled = enabled;
      const patch = enabled
        ? {
            tunnelEnabled: true,
            tunnelMode: settingsForm.tunnelMode,
            tunnelHostname: settingsForm.tunnelHostname,
            tunnelCredentialMode: settingsForm.tunnelCredentialMode,
            tunnelCredentialsFile: settingsForm.tunnelCredentialsFile,
            tunnelId: settingsForm.tunnelId,
            tunnelToken: settingsForm.tunnelToken,
            tunnelBinaryPath: settingsForm.tunnelBinaryPath,
            tunnelAutoRestart: settingsForm.tunnelAutoRestart
          }
        : { tunnelEnabled: false };
      const next = await updateSettings(patch);
      settings.value = next;
      tunnelStatus.value = enabled ? await startTunnel() : await stopTunnel();
      updateFeedback(enabled ? "隧道已启动。" : "隧道已停止。", "success");
    } catch (error) {
      // 失败时回滚开关，避免 UI 显示成已开启
      settingsForm.tunnelEnabled = !enabled;
      await refreshSettings().catch(() => undefined);
      toastError(error, enabled ? "隧道启动失败。" : "隧道停止失败。");
    } finally {
      await refreshTunnel();
      await refreshTunnelLogs();
    }
  });
}

export async function restartTunnelAction(): Promise<void> {
  await withBusy("tunnel-action", async () => {
    try {
      tunnelStatus.value = await restartTunnel();
      updateFeedback("隧道已重启。", "success");
    } catch (error) {
      toastError(error, "隧道重启失败。");
    } finally {
      await refreshTunnel();
      await refreshTunnelLogs();
    }
  });
}

/** corsRestricted 的一键修复：把隧道域名写进 CORS 允许来源并落库。 */
export async function addTunnelOriginToCors(): Promise<void> {
  const hostname = settingsForm.tunnelHostname.trim();
  if (!hostname) {
    return;
  }
  const origin = `https://${hostname}`;
  if (settingsForm.corsOrigins.includes(origin)) {
    return;
  }
  await withBusy("tunnel-action", async () => {
    try {
      const next = await updateSettings({ corsOrigins: [...settingsForm.corsOrigins, origin] });
      settings.value = next;
      settingsForm.corsOrigins = [...next.corsOrigins];
      updateFeedback(`已把 ${origin} 加入 CORS 允许来源。`, "success");
    } catch (error) {
      toastError(error, "写入 CORS 允许来源失败。");
    } finally {
      await refreshTunnel();
    }
  });
}

export function applyDiscoveredTunnel(tunnel: DiscoveredTunnel): void {
  settingsForm.tunnelMode = "named";
  settingsForm.tunnelCredentialMode = "credentials-file";
  settingsForm.tunnelId = tunnel.uuid;
  settingsForm.tunnelCredentialsFile = tunnel.credentialsFile;
  if (tunnel.hostname) {
    settingsForm.tunnelHostname = tunnel.hostname;
  }
  updateFeedback("已填入本机发现的隧道配置，保存后生效。", "info");
}
