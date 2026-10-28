<script setup lang="ts">
/**
 * MCP 接入。
 * 以前是左边一摞客户端大卡片、每张三个按钮（没安装的也摆着三个灰按钮），
 * 右边一栏「诊断结果」平时是空的。现在一行一个客户端，只给当前状态下能做的操作，
 * 没装的客户端排到最后；诊断结果直接长在那一行下面。
 */
import { computed } from "vue";

import type { AgentDetectionResult, AgentHost } from "@shared/index";

import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { copyText } from "../lib/actions";
import { agents, agentsLoaded, diagnostics, refreshAgents, runAgentAction } from "../stores/agents";
import { isBusy } from "../stores/busy";
import { saveSettingsFields, settingsForm, status } from "../stores/session";

async function saveDianaPath() { await saveSettingsFields(["dianaMcpConfigPath"]); await refreshAgents(); }

function hostBusy(host: AgentHost): boolean {
  return isBusy(`agent:install:${host}`) || isBusy(`agent:uninstall:${host}`) || isBusy(`agent:doctor:${host}`);
}

/** 已接入 → 检测到未接入 → 没装，同档保持接口给的顺序 */
const sortedAgents = computed(() =>
  [...agents.value].sort((left, right) => rank(left) - rank(right))
);

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
  <section class="page-shell">
    <div class="page-toolbar">
      <div class="toolbar-row flex-wrap">
        <div class="search-field min-w-[16rem]" title="AI 客户端连这个地址调用本应用">
          <span class="shrink-0 text-xs font-semibold text-[var(--muted)]">SSE 地址</span>
          <input :value="status?.mcpSseUrl ?? ''" readonly class="font-mono text-[13px]" aria-label="MCP 接入地址" placeholder="读取中…" />
        </div>
        <button
          type="button"
          class="btn-ghost shrink-0 px-3 py-1.5 text-sm font-semibold"
          :disabled="!status?.mcpSseUrl"
          @click="copyText(status?.mcpSseUrl ?? '')"
        >
          复制地址
        </button>
        <span class="toolbar-meta ml-auto">已接入 {{ installedCount }} · 检测到 {{ detectedCount }} 个客户端</span>
        <button type="button" class="icon-btn shrink-0" title="重新检测本机客户端" aria-label="重新检测本机客户端" @click="refreshAgents()">
          <AppNavIcon name="refresh" />
        </button>
      </div>
    </div>

    <div class="page-body">
      <p class="mb-3 px-1 text-xs text-[var(--muted)]">
        「接入」会把 Bangumi Cat 写进该客户端的 MCP 配置，之后在客户端里就能用自然语言查番、订阅、看下载。
      </p>

      <details class="mb-4 px-1 text-sm">
        <summary class="cursor-pointer">Diana 配置位置</summary>
        <p class="my-2 text-xs text-[var(--muted)]">默认自动检测。自定义部署填写 Diana 数据目录下 .mcp.json 的绝对路径；此接入方式要求 Diana 与 Bangumi Cat 在同一台机器、且能够运行同一个 Go 可执行文件。</p>
        <div class="flex gap-2"><input v-model="settingsForm.dianaMcpConfigPath" aria-label="Diana MCP 配置路径" class="settings-input flex-1" placeholder="留空自动检测" @keydown.enter="saveDianaPath" /><button class="btn-ghost" @click="saveDianaPath">保存并检测</button></div>
      </details>
      <div v-if="!agents.length" class="empty-state text-sm text-[var(--muted)]">
        {{ agentsLoaded() ? "没有检测到支持的 AI 客户端。装好 Diana / Codex / Claude / Cursor / Cherry Studio 后点右上角重新检测。" : "正在检测本机的 AI 客户端…" }}
      </div>

      <div v-else class="list-panel">
        <div v-for="agent in sortedAgents" :key="agent.host" :class="{ 'opacity-60': !agent.detected }">
          <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-3">
            <div class="grid min-w-0 gap-0.5">
              <div class="flex min-w-0 items-center gap-2">
                <strong class="text-sm font-semibold text-[var(--ink)]">{{ agent.displayName }}</strong>
                <span class="status-pill" :class="agentState(agent).className">{{ agentState(agent).label }}</span>
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

          <!-- 诊断结果就地展示在这个客户端下面 -->
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
    </div>
  </section>
</template>
