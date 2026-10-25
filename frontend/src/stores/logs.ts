import { ref } from "vue";

import type { LogEntry } from "@shared/index";

import { getLogFileInfo, listRecentLogs } from "../api";
import { t } from "../i18n";
import { withBusy } from "./busy";
import { toastError } from "./toasts";

export const logs = ref<LogEntry[]>([]);
export const logFilePath = ref("");

export async function refreshRecentLogs(limit = 200): Promise<void> {
  logs.value = await listRecentLogs(limit);
}

export async function refreshLogs(): Promise<void> {
  await withBusy("logs", async () => {
    try {
      const [nextLogs, nextFileInfo] = await Promise.allSettled([listRecentLogs(200), getLogFileInfo()]);
      if (nextLogs.status === "fulfilled") {
        logs.value = nextLogs.value;
      } else {
        throw nextLogs.reason;
      }
      if (nextFileInfo.status === "fulfilled") {
        logFilePath.value = nextFileInfo.value.path;
      }
    } catch (error) {
      toastError(error, t("logs.readFailed"));
    }
  });
}
