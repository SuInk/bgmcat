import { ref } from "vue";

import type { AgentDetectionResult, AgentDoctorResult, AgentHost } from "@shared/index";

import { detectAgents, doctorAgent, installAgent, uninstallAgent } from "../api";
import { t } from "../i18n";
import { withBusy } from "./busy";
import { toastError, updateFeedback } from "./toasts";

export const agents = ref<AgentDetectionResult[]>([]);
export const diagnostics = ref<Record<string, AgentDoctorResult | undefined>>({});

let detectionCompleted = false;

export function agentsLoaded(): boolean {
  return detectionCompleted;
}

export async function refreshAgents(): Promise<void> {
  agents.value = await detectAgents();
  detectionCompleted = true;
}

export async function runAgentAction(host: AgentHost, action: "install" | "uninstall" | "doctor"): Promise<void> {
  await withBusy(`agent:${action}:${host}`, async () => {
    try {
      if (action === "doctor") {
        const result = await doctorAgent(host);
        diagnostics.value = { ...diagnostics.value, [host]: result };
        updateFeedback(`已完成 ${host} 诊断。`, "success");
      } else {
        const result = action === "install" ? await installAgent(host) : await uninstallAgent(host);
        updateFeedback(result.message ?? `已完成 ${host} ${action === "install" ? "安装" : "卸载"}。`, "success");
        await refreshAgents();
      }
    } catch (error) {
      toastError(error, t("agent.actionFailed"));
    }
  });
}
