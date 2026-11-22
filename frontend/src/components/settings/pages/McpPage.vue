<script setup lang="ts">
import { computed, inject, watch } from "vue";

import type { AgentDetectionResult, AgentHost } from "@shared/index";

import AppNavIcon from "../../layout/AppNavIcon.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { settingsPageVisibleKey } from "../context";
import { copyText, openExternalLink } from "../../../lib/actions";
import { agents, agentsLoaded, diagnostics, refreshAgents, runAgentAction } from "../../../stores/agents";
import { isBusy } from "../../../stores/busy";
import { saveSettingsFields, settingsForm, status } from "../../../stores/session";

const DIANA_HOME_URL = "https://suink.github.io/Diana/";

const isPageVisible = inject(settingsPageVisibleKey, () => true);
const visible = computed(() => isPageVisible("mcp"));

watch(
  visible,
  (shown) => {
    if (shown && !agentsLoaded()) {
      void refreshAgents().catch(() => undefined);
    }
  },
  { immediate: true }
);

async function saveDianaPath(): Promise<void> {
  await saveSettingsFields(["dianaMcpConfigPath"]);
  await refreshAgents();
}

function hostBusy(host: AgentHost): boolean {
  return isBusy(`agent:install:${host}`) || isBusy(`agent:uninstall:${host}`) || isBusy(`agent:doctor:${host}`);
}

/** 已接入 → 检测到未接入 → 没装，同档保持接口给的顺序 */
const sortedAgents = computed(() => [...agents.value].sort((left, right) => rank(left) - rank(right)));

function rank(agent: AgentDetectionResult): number {
  if (agent.installed) {
    return 0;
  }
  return agent.detected ? 1 : 2;
}

function agentState(agent: AgentDetectionResult): { label: string; className: string } {
  if (agent.installed) {
    return { label: "已接入", className: "status-pill-ok" };
  }
  if (agent.detected) {
    return { label: "未接入", className: "status-pill-warn" };
  }
  return { label: "未安装", className: "status-pill-muted" };
}

const installedCount = computed(() => agents.value.filter((agent) => agent.installed).length);
const detectedCount = computed(() => agents.value.filter((agent) => agent.detected).length);
</script>

<template>
  <SettingsSection id="mcp" title="接入配置">
    <div class="settings-group">
      <SettingsItem
        stacked
        title="SSE 接入地址"
        desc="将此地址接入 AI 客户端后，即可用自然语言查番、订阅和查看下载。"
      >
        <div class="flex w-full gap-2">
          <input
            :value="status?.mcpSseUrl ?? ''"
            readonly
            aria-label="MCP 接入地址"
            class="settings-input flex-1 font-mono text-[13px]"
            placeholder="读取中…"
          />
          <button
            type="button"
            class="settings-secondary-button shrink-0"
            :disabled="!status?.mcpSseUrl"
            @click="copyText(status?.mcpSseUrl ?? '')"
          >
            复制地址
          </button>
        </div>
      </SettingsItem>

      <SettingsItem
        stacked
        title="Diana 配置路径"
        desc="留空自动检测，或填写 Diana 数据目录下 .mcp.json 绝对路径。"
      >
        <div class="flex w-full flex-wrap items-center gap-2">
          <input
            v-model="settingsForm.dianaMcpConfigPath"
            aria-label="Diana MCP 配置路径"
            class="settings-input min-w-[14rem] flex-1"
            placeholder="留空自动检测"
            @keydown.enter="saveDianaPath"
          />
          <button type="button" class="settings-secondary-button shrink-0" @click="saveDianaPath">
            保存并检测
          </button>
          <button
            type="button"
            class="settings-secondary-button inline-flex shrink-0 items-center gap-1"
            @click="openExternalLink(DIANA_HOME_URL)"
          >
            <span>Diana 官网</span>
            <AppNavIcon name="external" class="size-3.5 opacity-80" aria-hidden="true" />
          </button>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="mcp-clients" title="AI 客户端">
    <div class="mb-2 flex flex-wrap items-center justify-between gap-2 px-0.5 text-xs text-[var(--muted)]">
      <span>已接入 {{ installedCount }} · 检测到 {{ detectedCount }} 个客户端</span>
      <button
        type="button"
        class="settings-secondary-button inline-flex items-center gap-1.5"
        @click="refreshAgents()"
      >
        <AppNavIcon name="refresh" class="size-3.5" aria-hidden="true" />
        <span>重新检测</span>
      </button>
    </div>

    <div v-if="!agents.length" class="rounded-xl border border-[var(--line)] bg-[var(--card)] px-4 py-6 text-center text-sm text-[var(--muted)]">
      {{ agentsLoaded() ? "没有检测到支持的 AI 客户端。装好 Diana / Codex / Claude / Cursor / Cherry Studio 后点右上角重新检测。" : "正在检测本机的 AI 客户端…" }}
    </div>

    <div v-else class="list-panel">
      <div v-for="agent in sortedAgents" :key="agent.host" :class="{ 'opacity-65': !agent.detected }">
        <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-3">
          <div class="grid min-w-0 gap-0.5">
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <strong class="text-sm font-semibold text-[var(--ink)]">{{ agent.displayName }}</strong>
              <span class="status-pill" :class="agentState(agent).className">{{ agentState(agent).label }}</span>
              <button
                v-if="agent.host === 'diana'"
                type="button"
                class="inline-flex items-center gap-0.5 text-xs font-medium text-[var(--accent-vivid)] hover:underline"
                :title="DIANA_HOME_URL"
                @click="openExternalLink(DIANA_HOME_URL)"
              >
                <span>官网</span>
                <AppNavIcon name="external" class="size-3" aria-hidden="true" />
              </button>
            </div>
            <p class="truncate font-mono text-[11px] text-[var(--muted)]" :title="agent.configPath">{{ agent.configPath }}</p>
            <p v-if="agent.details" class="text-xs text-[var(--muted)]">{{ agent.details }}</p>
          </div>

          <div v-if="agent.detected" class="flex shrink-0 flex-wrap justify-end gap-1.5">
            <button
              v-if="!agent.installed"
              type="button"
              class="btn-primary px-3 py-1.5 text-xs font-semibold"
              :disabled="hostBusy(agent.host)"
              @click="runAgentAction(agent.host, 'install')"
            >
              {{ isBusy(`agent:install:${agent.host}`) ? "接入中…" : "接入" }}
            </button>
            <template v-else>
              <button
                type="button"
                class="btn-ghost px-3 py-1.5 text-xs font-semibold"
                :disabled="hostBusy(agent.host)"
                @click="runAgentAction(agent.host, 'doctor')"
              >
                {{ isBusy(`agent:doctor:${agent.host}`) ? "诊断中…" : "诊断" }}
              </button>
              <button
                type="button"
                class="btn-ghost px-3 py-1.5 text-xs font-semibold"
                :disabled="hostBusy(agent.host)"
                @click="runAgentAction(agent.host, 'install')"
              >
                {{ isBusy(`agent:install:${agent.host}`) ? "安装中…" : "重新安装" }}
              </button>
              <button
                type="button"
                class="btn-ghost px-3 py-1.5 text-xs font-semibold text-[var(--muted)] hover:!border-[var(--err)] hover:!text-[var(--err)]"
                :disabled="hostBusy(agent.host)"
                @click="runAgentAction(agent.host, 'uninstall')"
              >
                {{ isBusy(`agent:uninstall:${agent.host}`) ? "卸载中…" : "卸载" }}
              </button>
            </template>
          </div>
        </div>

        <div
          v-if="diagnostics[agent.host]"
          class="mx-4 mb-3 grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--tint-1)] px-3 py-2.5"
        >
          <div class="flex flex-wrap items-center gap-2">
            <span class="status-pill" :class="diagnostics[agent.host]!.endpointReachable ? 'status-pill-ok' : 'status-pill-err'">
              {{ diagnostics[agent.host]!.endpointReachable ? "地址可连通" : "地址连不上" }}
            </span>
            <span v-if="diagnostics[agent.host]!.message" class="text-xs text-[var(--muted)]">{{ diagnostics[agent.host]!.message }}</span>
          </div>
          <ul v-if="diagnostics[agent.host]!.issues.length" class="grid gap-1 text-xs text-[var(--ink)]">
            <li v-for="issue in diagnostics[agent.host]!.issues" :key="issue" class="flex gap-2">
              <span class="text-[var(--warn)]">•</span>{{ issue }}
            </li>
          </ul>
          <p v-else class="text-xs text-[var(--muted)]">配置没有发现问题。</p>
        </div>
      </div>
    </div>
  </SettingsSection>
</template>
