<script setup lang="ts">
import { computed, inject, nextTick, onUnmounted, ref, watch } from "vue";

import {
  addNetworkTarget,
  deleteNetworkTarget,
  getNetworkTarget,
  listNetworkTargets,
  patchNetworkTarget,
  probeNetworkTargetIP,
  scanNetworkTarget
} from "../../api";
import type {
  DriveNetworkResultRow,
  DriveNetworkStatus,
  NetworkProbeTargetStatus,
  NetworkProbeTargetSummary
} from "../../api";
import { copyText } from "../../lib/actions";
import { toastError, updateFeedback } from "../../stores/toasts";
import AppSelect from "../common/AppSelect.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";
import { requestedProbeTarget, settingsPageVisibleKey } from "./context";
import SettingsItem from "./SettingsItem.vue";

const targets = ref<NetworkProbeTargetSummary[]>([]);
const activeTargetId = ref("google");
const activeStatus = ref<NetworkProbeTargetStatus | null>(null);

const addingCustom = ref(false);
const customHostDraft = ref("");
const customHostInputRef = ref<HTMLInputElement | null>(null);
const customAddingBusy = ref(false);

const candidateDraft = ref("");
const singleProbeIP = ref("");
const sniMode = ref<DriveNetworkStatus["sniMode"]>("original");
const sniHost = ref("");
const scanning = ref(false);
const saving = ref(false);
const probingIP = ref<string | null>(null);
const resultFilter = ref<"all" | "available" | "failed">("all");
const scanElapsedSec = ref(0);
let scanTimer: ReturnType<typeof setInterval> | null = null;

const locked = computed(
  () => !activeStatus.value || scanning.value || saving.value || probingIP.value !== null
);

const presetSeedsText = computed(() => (activeStatus.value?.presetSeeds ?? []).join("\n"));

const allResults = computed(() => activeStatus.value?.results ?? []);
const availableResults = computed(() =>
  allResults.value.filter((r) => r.available && Date.now() / 1000 - r.verifiedAt < 86400)
);
const failedResults = computed(() =>
  allResults.value.filter((r) => !r.available || Date.now() / 1000 - r.verifiedAt >= 86400)
);

const filteredResults = computed(() => {
  if (resultFilter.value === "available") return availableResults.value;
  if (resultFilter.value === "failed") return failedResults.value;
  return allResults.value;
});

const fastestLatencyMs = computed(() => {
  if (!availableResults.value.length) return null;
  return Math.min(...availableResults.value.map((r) => r.latencyMs));
});

function shortTargetName(name: string): string {
  return name.replace(/^Microsoft\s+/iu, "");
}

function syncSummaryFromStatus(status: NetworkProbeTargetStatus): void {
  const nowSec = Date.now() / 1000;
  const avail = status.results.filter((r) => r.available && nowSec - r.verifiedAt < 86400);
  const fastest = avail.length ? Math.min(...avail.map((r) => r.latencyMs)) : null;
  const nextItem: NetworkProbeTargetSummary = {
    id: status.id,
    name: status.name,
    group: status.group,
    hosts: status.hosts,
    custom: status.custom,
    enabled: status.enabled,
    selectedIP: status.selectedIP,
    availableCount: avail.length,
    totalCount: status.results.length,
    fastestMs: fastest
  };
  const idx = targets.value.findIndex((t) => t.id === status.id);
  if (idx >= 0) {
    targets.value.splice(idx, 1, nextItem);
  } else {
    targets.value.push(nextItem);
  }
}

function applyStatus(value: NetworkProbeTargetStatus): void {
  activeStatus.value = value;
  activeTargetId.value = value.id;
  sniMode.value = value.sniMode;
  sniHost.value = value.sniHost;
  syncSummaryFromStatus(value);
}

async function loadTargetsAndSelect(targetId = activeTargetId.value): Promise<void> {
  try {
    const list = await listNetworkTargets();
    targets.value = list;
    const chosen = list.some((t) => t.id === targetId) ? targetId : (list[0]?.id ?? "google");
    activeTargetId.value = chosen;
    applyStatus(await getNetworkTarget(chosen));
  } catch (error) {
    toastError(error, "读取直连探测状态失败");
  }
}

async function selectTarget(id: string): Promise<void> {
  if (scanning.value || saving.value) return;
  if (id === activeTargetId.value && activeStatus.value) return;
  activeTargetId.value = id;
  candidateDraft.value = "";
  singleProbeIP.value = "";
  resultFilter.value = "all";
  try {
    applyStatus(await getNetworkTarget(id));
  } catch (error) {
    toastError(error, "切换探测目标失败");
  }
}

async function openAddCustom(): Promise<void> {
  addingCustom.value = true;
  customHostDraft.value = "";
  await nextTick();
  customHostInputRef.value?.focus();
}

async function submitCustomTarget(): Promise<void> {
  const host = customHostDraft.value.trim();
  if (!host || customAddingBusy.value) return;
  customAddingBusy.value = true;
  try {
    const created = await addNetworkTarget(host);
    addingCustom.value = false;
    customHostDraft.value = "";
    candidateDraft.value = "";
    applyStatus(created);
  } catch (error) {
    toastError(error, "添加域名失败");
  } finally {
    customAddingBusy.value = false;
  }
}

async function handleDeleteCustom(target: NetworkProbeTargetSummary): Promise<void> {
  if (!target.custom || scanning.value || saving.value) return;
  saving.value = true;
  try {
    await deleteNetworkTarget(target.id);
    targets.value = targets.value.filter((t) => t.id !== target.id);
    if (activeTargetId.value === target.id) {
      const nextId = targets.value[0]?.id ?? "google";
      activeTargetId.value = nextId;
      applyStatus(await getNetworkTarget(nextId));
    }
  } catch (error) {
    toastError(error, "删除域名失败");
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement;
  saving.value = true;
  try {
    applyStatus(await patchNetworkTarget(activeTargetId.value, { enabled: input.checked }));
  } catch (error) {
    input.checked = activeStatus.value?.enabled ?? false;
    toastError(error, "保存直连开关失败");
  } finally {
    saving.value = false;
  }
}

async function toggleBypassTun(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement;
  saving.value = true;
  try {
    applyStatus(await patchNetworkTarget(activeTargetId.value, { bypassTun: input.checked }));
  } catch (error) {
    input.checked = activeStatus.value?.bypassTun ?? true;
    toastError(error, "保存物理网卡设置失败");
  } finally {
    saving.value = false;
  }
}

async function saveTLSPolicy(): Promise<void> {
  if (sniMode.value === "custom" && !sniHost.value.trim()) {
    return;
  }
  saving.value = true;
  try {
    applyStatus(
      await patchNetworkTarget(activeTargetId.value, {
        sniMode: sniMode.value,
        sniHost: sniHost.value
      })
    );
  } catch (error) {
    toastError(error, "保存握手策略失败");
  } finally {
    saving.value = false;
  }
}

function onSNIModeChange(): void {
  if (sniMode.value !== "custom") {
    void saveTLSPolicy();
  }
}

function startScanTimer(): void {
  scanElapsedSec.value = 0;
  if (scanTimer) clearInterval(scanTimer);
  scanTimer = setInterval(() => {
    scanElapsedSec.value += 1;
  }, 1000);
}

function stopScanTimer(): void {
  if (scanTimer) {
    clearInterval(scanTimer);
    scanTimer = null;
  }
}

async function runScan(customList?: string): Promise<void> {
  scanning.value = true;
  startScanTimer();
  try {
    if (customList !== undefined) {
      candidateDraft.value = customList;
    }
    if (
      activeStatus.value &&
      (sniMode.value !== activeStatus.value.sniMode || sniHost.value !== activeStatus.value.sniHost)
    ) {
      applyStatus(
        await patchNetworkTarget(activeTargetId.value, {
          sniMode: sniMode.value,
          sniHost: sniHost.value
        })
      );
    }
    const res = await scanNetworkTarget(activeTargetId.value, candidateDraft.value);
    applyStatus(res);
    const okCount = res.results.filter((r) => r.available).length;
    updateFeedback(
      okCount > 0 ? `发现 ${okCount} 个可用节点` : "未发现可用节点",
      okCount > 0 ? "success" : "info"
    );
  } catch (error) {
    toastError(error, "节点扫描失败");
  } finally {
    stopScanTimer();
    scanning.value = false;
  }
}

async function handleProbeSingle(ipToProbe?: string): Promise<void> {
  const target = (ipToProbe ?? singleProbeIP.value).trim();
  if (!target) return;
  probingIP.value = target;
  try {
    const res = await probeNetworkTargetIP(activeTargetId.value, target);
    applyStatus(res);
    const row = res.results.find((r) => r.ip === target);
    if (row?.available) {
      updateFeedback(`${target} 可用（${row.latencyMs} ms）`, "success");
      if (!ipToProbe) singleProbeIP.value = "";
    } else {
      updateFeedback(`${target} 不可用：${row?.error || "连接失败"}`, "error");
    }
  } catch (error) {
    toastError(error, `探测 ${target} 失败`);
  } finally {
    probingIP.value = null;
  }
}

async function selectPreferredIP(ip: string): Promise<void> {
  saving.value = true;
  try {
    const nextPref = activeStatus.value?.preferredIP === ip ? "" : ip;
    const res = await patchNetworkTarget(activeTargetId.value, {
      enabled: true,
      preferredIP: nextPref
    });
    applyStatus(res);
    updateFeedback(nextPref ? `已锁定 ${nextPref}` : "已恢复自动择优", "success");
  } catch (error) {
    toastError(error, "切换节点失败");
  } finally {
    saving.value = false;
  }
}

async function handleCopyIP(ip: string): Promise<void> {
  await copyText(ip);
}

function sniBadgeLabel(sni?: string): string {
  if (!sni) return "原始 SNI";
  if (sni === "ech") return "TLS 1.3 ECH";
  if (sni === "fragment") return "TCP/TLS 分片";
  if (sni.startsWith("mirror:")) return `${sni.slice(7)} + 分片`;
  if (sni === "none") return "无 SNI + 分片";
  return `SNI: ${sni}`;
}

function latencyPercent(ms: number): number {
  if (ms <= 0) return 8;
  return Math.max(8, Math.min(100, Math.round((ms / 1800) * 100)));
}

function latencyColorClass(ms: number): string {
  if (ms < 450) return "probe-bar-fast";
  if (ms < 950) return "probe-bar-medium";
  return "probe-bar-slow";
}

function resultStatusLabel(result: DriveNetworkResultRow): string {
  if (!result.available) {
    return result.error || "连接失败";
  }
  if (Date.now() / 1000 - result.verifiedAt >= 86400) {
    return "已过期，请复测";
  }
  return result.warning || "校验通过";
}

const isPageVisible = inject(settingsPageVisibleKey, () => true);
const probeVisible = computed(() => isPageVisible("network") || isPageVisible("probe"));
let initialLoaded = false;

async function handleRequestedTarget(req: { id: string; host?: string; name?: string }): Promise<void> {
  if (!initialLoaded) {
    initialLoaded = true;
    await loadTargetsAndSelect(req.id);
  }
  if (targets.value.some((t) => t.id === req.id)) {
    await selectTarget(req.id);
    return;
  }
  if (req.host) {
    try {
      const created = await addNetworkTarget(req.host, req.name);
      applyStatus(created);
    } catch (error) {
      toastError(error, "定位探测目标失败");
    }
  }
}

watch(
  probeVisible,
  (visible) => {
    if (visible && !initialLoaded) {
      initialLoaded = true;
      const pending = requestedProbeTarget.value;
      if (pending) {
        requestedProbeTarget.value = null;
        void handleRequestedTarget(pending);
      } else {
        void loadTargetsAndSelect();
      }
    }
  },
  { immediate: true }
);

watch(requestedProbeTarget, (req) => {
  if (!req) return;
  requestedProbeTarget.value = null;
  void handleRequestedTarget(req);
});

onUnmounted(() => {
  stopScanTimer();
});
</script>

<template>
  <div class="probe-workbench">
    <!-- 目标选择栏 -->
    <div class="probe-target-bar" role="tablist" aria-label="探测目标">
      <div
        v-for="target in targets"
        :key="target.id"
        class="probe-target-chip"
        :class="{ 'is-active': activeTargetId === target.id }"
      >
        <button
          type="button"
          role="tab"
          class="probe-target-btn"
          :aria-selected="activeTargetId === target.id"
          @click="selectTarget(target.id)"
        >
          <span
            class="probe-dot"
            :class="target.enabled && target.selectedIP ? 'probe-dot-ok' : 'probe-dot-idle'"
          />
          <span>{{ shortTargetName(target.name) }}</span>
        </button>
        <button
          v-if="target.custom"
          type="button"
          class="probe-target-del"
          :title="`移除 ${target.name}`"
          aria-label="移除自定义域名"
          @click.stop="handleDeleteCustom(target)"
        >
          <AppNavIcon name="close" class="size-3" />
        </button>
      </div>

      <form v-if="addingCustom" class="probe-add-form" @submit.prevent="submitCustomTarget">
        <input
          ref="customHostInputRef"
          v-model="customHostDraft"
          class="settings-input probe-add-input"
          placeholder="域名，如 bangumi.moe"
          spellcheck="false"
          @keydown.esc.stop="addingCustom = false"
          @keydown.enter.stop
        />
        <button
          type="submit"
          class="probe-action-btn is-pinned"
          :disabled="!customHostDraft.trim() || customAddingBusy"
        >
          添加
        </button>
        <button type="button" class="probe-action-btn" @click="addingCustom = false">
          取消
        </button>
      </form>
      <button
        v-else
        type="button"
        class="probe-target-add"
        @click="openAddCustom"
      >
        <AppNavIcon name="plus" class="size-3.5" />
        <span>域名</span>
      </button>
    </div>

    <!-- 顶部 4 格实时状态 -->
    <div class="settings-summary-grid">
      <div class="settings-summary-item">
        <span class="settings-summary-label">当前路由</span>
        <strong class="flex items-center gap-1.5 truncate">
          <span
            class="probe-dot"
            :class="activeStatus?.enabled && activeStatus?.selectedIP ? 'probe-dot-ok' : 'probe-dot-idle'"
          />
          {{
            activeStatus?.enabled
              ? activeStatus.selectedIP || "回退系统网络"
              : "系统网络"
          }}
        </strong>
      </div>
      <div class="settings-summary-item">
        <span class="settings-summary-label">物理网卡</span>
        <strong class="truncate">
          {{
            activeStatus?.bypassTun ?? true
              ? `已绑定 ${activeStatus?.interface || "默认网卡"}`
              : "未绑定（允许 TUN）"
          }}
        </strong>
      </div>
      <div class="settings-summary-item">
        <span class="settings-summary-label">TLS 策略</span>
        <strong class="truncate">
          {{
            activeStatus?.selectedIP
              ? sniBadgeLabel(activeStatus.selectedSNI)
              : sniMode === "original"
                ? "自适应分片"
                : sniBadgeLabel(sniMode)
          }}
        </strong>
      </div>
      <div class="settings-summary-item">
        <span class="settings-summary-label">可用节点</span>
        <strong class="truncate">
          {{ availableResults.length }} / {{ allResults.length }}
          <template v-if="fastestLatencyMs !== null"> · {{ fastestLatencyMs }} ms</template>
        </strong>
      </div>
    </div>

    <p v-if="activeStatus?.tunWarning" class="tunnel-check tunnel-check-warn">
      <strong>提醒</strong>
      <span>{{ activeStatus.tunWarning }}</span>
    </p>

    <!-- 开关与握手策略 -->
    <div class="settings-group">
      <SettingsItem
        :title="`${activeStatus?.name || '目标'} 直连`"
        :desc="activeStatus?.hosts?.join(' · ')"
      >
        <input
          type="checkbox"
          class="settings-switch"
          :checked="activeStatus?.enabled ?? false"
          :disabled="!activeStatus || saving"
          @change="toggleEnabled"
        />
      </SettingsItem>

      <SettingsItem
        title="绕过 TUN（绑定物理网卡）"
        :desc="activeStatus?.interface || undefined"
      >
        <input
          type="checkbox"
          class="settings-switch"
          :checked="activeStatus?.bypassTun ?? true"
          :disabled="!activeStatus || saving"
          @change="toggleBypassTun"
        />
      </SettingsItem>

      <SettingsItem as="div" title="TLS 握手">
        <div class="flex flex-wrap items-center justify-end gap-2">
          <AppSelect
            align="right"
            :model-value="sniMode"
            aria-label="TLS 握手策略"
            :disabled="scanning || saving"
            :options="[
              { value: 'original', label: '自适应（推荐）' },
              { value: 'ech', label: 'TLS 1.3 ECH' },
              { value: 'fragment', label: 'TCP/TLS 分片' },
              { value: 'none', label: '无 SNI + 分片' },
              { value: 'custom', label: '自定义 SNI' }
            ]"
            @update:model-value="sniMode = $event as typeof sniMode"
            @change="onSNIModeChange"
          />
          <template v-if="sniMode === 'custom'">
            <input
              v-model="sniHost"
              aria-label="自定义 SNI 域名"
              class="settings-input settings-input-short"
              placeholder="如 g.cn"
              :disabled="scanning || saving"
              @keydown.enter.prevent.stop="saveTLSPolicy"
            />
            <button
              type="button"
              class="settings-secondary-button"
              :disabled="locked || !sniHost.trim()"
              @click="saveTLSPolicy"
            >
              应用
            </button>
          </template>
        </div>
      </SettingsItem>
    </div>

    <!-- 探测与节点池 -->
    <div class="settings-group">
      <SettingsItem as="div" stacked title="节点探测">
        <div class="grid w-full min-w-0 gap-3">
          <div class="probe-control-bar">
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                class="probe-primary-btn"
                :disabled="locked"
                @click="runScan()"
              >
                {{ scanning ? `扫描中（${scanElapsedSec}s）…` : "扫描节点" }}
              </button>
              <button
                v-if="presetSeedsText"
                type="button"
                class="settings-secondary-button"
                :disabled="locked"
                @click="runScan(presetSeedsText)"
              >
                优选速测
              </button>
              <button
                v-if="candidateDraft.trim()"
                type="button"
                class="settings-secondary-button"
                :disabled="locked"
                @click="runScan('')"
              >
                自动扫描
              </button>
            </div>

            <div class="probe-single-box">
              <input
                v-model="singleProbeIP"
                class="settings-input probe-single-input"
                placeholder="单 IP 诊断"
                :disabled="locked"
                @keydown.enter.prevent.stop="handleProbeSingle()"
              />
              <button
                type="button"
                class="settings-secondary-button shrink-0"
                :disabled="locked || !singleProbeIP.trim()"
                @click="handleProbeSingle()"
              >
                {{ probingIP && probingIP === singleProbeIP.trim() ? "诊断中…" : "诊断" }}
              </button>
            </div>
          </div>

          <div v-if="scanning" class="probe-progress-card" role="status" aria-live="polite">
            <div class="probe-progress-bar">
              <span class="probe-progress-fill" />
            </div>
          </div>

          <div class="probe-custom-details grid gap-2">
            <span class="font-semibold text-[var(--ink-secondary)]">
              自定义候选 IP{{ candidateDraft.trim() ? "（已填）" : "" }}
            </span>
            <textarea
              v-model="candidateDraft"
              aria-label="自定义候选 IP"
              class="settings-input settings-input-wide font-mono text-xs"
              rows="3"
              placeholder="每行一个公网 IP，留空自动解析与采样"
              @keydown.enter.stop
            />
            <div v-if="presetSeedsText || candidateDraft" class="flex flex-wrap gap-2">
              <button
                v-if="presetSeedsText"
                type="button"
                class="settings-secondary-button"
                :disabled="locked"
                @click="candidateDraft = presetSeedsText"
              >
                填入优选
              </button>
              <button
                v-if="candidateDraft"
                type="button"
                class="settings-secondary-button"
                :disabled="locked"
                @click="candidateDraft = ''"
              >
                清空
              </button>
            </div>
          </div>
        </div>
      </SettingsItem>

      <SettingsItem as="div" stacked>
        <div class="probe-results-head">
          <div class="flex flex-wrap items-center gap-2">
            <span class="settings-title">节点列表</span>
            <span v-if="activeStatus?.scannedAt" class="settings-desc !mt-0">
              {{ new Date(activeStatus.scannedAt).toLocaleTimeString() }}
            </span>
          </div>

          <div class="flex flex-wrap items-center gap-1.5">
            <button
              v-if="activeStatus?.preferredIP"
              type="button"
              class="probe-filter-pill probe-filter-pill-warn"
              :disabled="locked"
              @click="selectPreferredIP(activeStatus.preferredIP)"
            >
              锁定 {{ activeStatus.preferredIP }} ×
            </button>
            <button
              type="button"
              class="probe-filter-pill"
              :class="{ 'is-active': resultFilter === 'all' }"
              @click="resultFilter = 'all'"
            >
              全部 ({{ allResults.length }})
            </button>
            <button
              type="button"
              class="probe-filter-pill"
              :class="{ 'is-active': resultFilter === 'available' }"
              @click="resultFilter = 'available'"
            >
              可用 ({{ availableResults.length }})
            </button>
            <button
              type="button"
              class="probe-filter-pill"
              :class="{ 'is-active': resultFilter === 'failed' }"
              @click="resultFilter = 'failed'"
            >
              失败 ({{ failedResults.length }})
            </button>
          </div>
        </div>

        <div v-if="filteredResults.length" class="probe-node-list mt-2">
          <div
            v-for="row in filteredResults"
            :key="row.ip"
            class="probe-node-row"
            :class="{
              'is-selected': activeStatus?.selectedIP === row.ip,
              'is-unavailable': !row.available
            }"
          >
            <div class="probe-node-main">
              <div class="flex flex-wrap items-center gap-2">
                <span class="probe-dot" :class="row.available ? 'probe-dot-ok' : 'probe-dot-err'" />
                <span class="font-mono text-sm font-semibold text-[var(--ink)]">{{ row.ip }}</span>
                <span v-if="activeStatus?.selectedIP === row.ip" class="probe-badge probe-badge-active">
                  生效中
                </span>
                <span v-if="activeStatus?.preferredIP === row.ip" class="probe-badge probe-badge-pinned">
                  锁定
                </span>
                <span v-if="row.available" class="probe-badge probe-badge-sni">
                  {{ sniBadgeLabel(row.sni) }}
                </span>
              </div>
              <p class="probe-node-status" :class="row.available ? 'text-[var(--muted)]' : 'text-[var(--err)]'">
                {{ resultStatusLabel(row) }}
              </p>
            </div>

            <div v-if="row.available" class="probe-node-metrics">
              <div class="probe-latency-head">
                <span v-if="row.tcpLatencyMs && row.tcpLatencyMs > 0" class="text-[11px] text-[var(--muted)]">
                  TCP {{ row.tcpLatencyMs }} ms ·
                </span>
                <strong class="font-mono text-xs text-[var(--ink)]">{{ row.latencyMs }} ms</strong>
              </div>
              <div class="probe-bar-track">
                <span
                  class="probe-bar-fill"
                  :class="latencyColorClass(row.latencyMs)"
                  :style="{ width: `${latencyPercent(row.latencyMs)}%` }"
                />
              </div>
            </div>

            <div class="probe-node-actions">
              <button
                v-if="row.available"
                type="button"
                class="probe-action-btn"
                :class="{ 'is-pinned': activeStatus?.preferredIP === row.ip }"
                :disabled="locked"
                @click="selectPreferredIP(row.ip)"
              >
                {{ activeStatus?.preferredIP === row.ip ? "解锁" : "锁定" }}
              </button>
              <button
                type="button"
                class="probe-action-btn"
                :disabled="locked"
                @click="handleProbeSingle(row.ip)"
              >
                {{ probingIP === row.ip ? "复测中…" : "复测" }}
              </button>
              <button
                type="button"
                class="probe-action-btn"
                @click="handleCopyIP(row.ip)"
              >
                复制
              </button>
            </div>
          </div>
        </div>

        <div v-else class="probe-empty-state mt-2">
          <p>{{ allResults.length ? "无匹配节点" : "暂无节点，点击上方「扫描节点」" }}</p>
        </div>
      </SettingsItem>
    </div>
  </div>
</template>

<style scoped>
.probe-workbench {
  display: grid;
  gap: 0.75rem;
}

.probe-target-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
}

.probe-target-chip {
  display: inline-flex;
  align-items: center;
  border-radius: 999px;
  border: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
  background: var(--settings-block);
  transition: border-color 140ms ease, background 140ms ease;
}

.probe-target-chip.is-active {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.probe-target-btn {
  display: inline-flex;
  height: 1.95rem;
  align-items: center;
  gap: 0.42rem;
  padding: 0 0.78rem;
  color: var(--ink-secondary);
  font-size: 0.78rem;
  font-weight: 600;
}

.probe-target-chip.is-active .probe-target-btn {
  color: var(--accent);
}

.probe-target-del {
  display: inline-flex;
  height: 1.95rem;
  align-items: center;
  padding: 0 0.5rem 0 0.1rem;
  color: var(--muted);
}

.probe-target-del:hover {
  color: var(--err);
}

.probe-target-add {
  display: inline-flex;
  height: 1.95rem;
  align-items: center;
  gap: 0.25rem;
  border-radius: 999px;
  border: 1px dashed color-mix(in srgb, var(--line) 95%, transparent);
  padding: 0 0.72rem;
  color: var(--muted);
  font-size: 0.76rem;
  font-weight: 600;
  transition: border-color 140ms ease, color 140ms ease;
}

.probe-target-add:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.probe-add-form {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}

.probe-add-input {
  width: 11.5rem;
  min-height: 1.95rem;
  padding: 0.2rem 0.65rem;
  font-size: 0.78rem;
}

.probe-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 999px;
  flex-shrink: 0;
}

.probe-dot-ok {
  background: var(--ok, #10b981);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--ok, #10b981) 22%, transparent);
}

.probe-dot-err {
  background: var(--err, #ef4444);
}

.probe-dot-idle {
  background: color-mix(in srgb, var(--muted) 55%, transparent);
}

.probe-control-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.625rem;
}

.probe-primary-btn {
  min-height: 2.125rem;
  border-radius: 10px;
  background: var(--accent);
  padding: 0 1.05rem;
  color: var(--accent-contrast, #fff);
  font-size: 0.8125rem;
  font-weight: 600;
  transition: opacity 150ms ease;
}

.probe-primary-btn:hover:not(:disabled) {
  opacity: 0.92;
}

.probe-primary-btn:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.probe-single-box {
  display: flex;
  flex: 1 1 15rem;
  max-width: 22rem;
  align-items: center;
  gap: 0.5rem;
}

.probe-single-input {
  width: 100%;
  min-width: 0;
  flex: 1;
}

.probe-progress-card {
  border-radius: 999px;
  overflow: hidden;
}

.probe-progress-bar {
  height: 4px;
  width: 100%;
  overflow: hidden;
  border-radius: 999px;
  background: color-mix(in srgb, var(--ink) 10%, transparent);
}

.probe-progress-fill {
  display: block;
  height: 100%;
  width: 42%;
  border-radius: 999px;
  background: var(--accent);
  animation: probe-slide 1.25s ease-in-out infinite alternate;
}

@keyframes probe-slide {
  0% {
    transform: translateX(-15%);
  }
  100% {
    transform: translateX(155%);
  }
}

.probe-custom-details {
  font-size: 0.76rem;
  color: var(--muted);
}

.probe-custom-details > summary {
  cursor: pointer;
  font-weight: 600;
}

.probe-results-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.probe-filter-pill {
  border-radius: 8px;
  border: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
  background: var(--panel);
  padding: 0.18rem 0.6rem;
  color: var(--muted);
  font-size: 0.72rem;
  font-weight: 600;
  transition: all 140ms ease;
}

.probe-filter-pill.is-active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.probe-filter-pill-warn {
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
  background: var(--accent-soft);
  color: var(--accent);
}

.probe-node-list {
  display: grid;
  gap: 0.45rem;
  max-height: 24rem;
  overflow-y: auto;
  padding-right: 2px;
}

.probe-node-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  border-radius: 12px;
  border: 1px solid color-mix(in srgb, var(--line) 68%, transparent);
  background: var(--panel);
  padding: 0.6rem 0.8rem;
  transition: border-color 140ms ease, background 140ms ease;
}

.probe-node-row.is-selected {
  border-color: color-mix(in srgb, var(--ok, #10b981) 55%, transparent);
  background: color-mix(in srgb, var(--ok, #10b981) 7%, var(--panel));
}

.probe-node-row.is-unavailable {
  opacity: 0.72;
}

.probe-node-main {
  min-width: 13rem;
  flex: 1 1 15rem;
}

.probe-node-status {
  margin-top: 0.15rem;
  font-size: 0.72rem;
  line-height: 1.45;
}

.probe-badge {
  display: inline-flex;
  align-items: center;
  border-radius: 999px;
  padding: 0.08rem 0.5rem;
  font-size: 0.68rem;
  font-weight: 600;
}

.probe-badge-active {
  background: color-mix(in srgb, var(--ok, #10b981) 18%, transparent);
  color: var(--ok, #10b981);
}

.probe-badge-pinned {
  background: var(--accent-soft);
  color: var(--accent);
}

.probe-badge-sni {
  border: 1px solid color-mix(in srgb, var(--line) 80%, transparent);
  color: var(--muted);
}

.probe-node-metrics {
  display: grid;
  width: 8.5rem;
  gap: 0.28rem;
}

.probe-latency-head {
  display: flex;
  align-items: baseline;
  justify-content: flex-end;
  gap: 0.25rem;
}

.probe-bar-track {
  height: 5px;
  width: 100%;
  overflow: hidden;
  border-radius: 999px;
  background: color-mix(in srgb, var(--ink) 10%, transparent);
}

.probe-bar-fill {
  display: block;
  height: 100%;
  border-radius: 999px;
}

.probe-bar-fast {
  background: var(--ok, #10b981);
}

.probe-bar-medium {
  background: var(--warn, #f59e0b);
}

.probe-bar-slow {
  background: var(--err, #ef4444);
}

.probe-node-actions {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}

.probe-action-btn {
  border-radius: 8px;
  border: 1px solid color-mix(in srgb, var(--line) 75%, transparent);
  background: var(--settings-block);
  padding: 0.22rem 0.62rem;
  color: var(--ink);
  font-size: 0.72rem;
  font-weight: 600;
  transition: border-color 140ms ease, background 140ms ease;
}

.probe-action-btn:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accent);
}

.probe-action-btn.is-pinned {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.probe-action-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.probe-empty-state {
  border-radius: 11px;
  border: 1px dashed color-mix(in srgb, var(--line) 80%, transparent);
  padding: 1.1rem;
  text-align: center;
  color: var(--muted);
  font-size: 0.78rem;
}
</style>
