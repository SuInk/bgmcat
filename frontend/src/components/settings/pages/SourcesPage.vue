<script setup lang="ts">
import { computed, inject, onMounted, reactive, ref } from "vue";
import { GripVertical } from "@lucide/vue";

import type { ResourceProviderSummary } from "@shared/index";

import {
  listMetadataPlatforms,
  listResourceProviders,
  testMetadataPlatform,
  testResourceProvider,
  type MetadataPlatformInfo
} from "../../../api";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { settingsOpenProbeKey } from "../context";
import { probeStatusClass, probeStatusLabel, type ProbeResult } from "../probe";
import { saveSettingsFields, settingsForm } from "../../../stores/session";
import { toastError } from "../../../stores/toasts";

const BUILTIN_PROBE_MAP: Record<string, string> = {
  mikan: "mikan",
  bangumimoe: "bangumimoe",
  dmhy: "dmhy",
  nyaa: "nyaa",
  tmdb: "tmdb"
};

const PLATFORM_DEFAULT_HOSTS: Record<string, string> = {
  bangumi: "api.bgm.tv",
  anilist: "graphql.anilist.co",
  mal: "api.jikan.moe",
  tmdb: "api.themoviedb.org"
};

const openProbe = inject(settingsOpenProbeKey, () => undefined);

const resourceProviders = ref<ResourceProviderSummary[]>([]);
const testingProviders = reactive(new Set<string>());
const testingAllProviders = ref(false);
const providerBatchDone = ref(0);
const providerBatchTotal = ref(0);
const providerResults = ref<Record<string, ProbeResult>>({});
const openMirrorId = ref<string | null>(null);
const mirrorDrafts = reactive<Record<string, string>>({});

const draggingProviderId = ref<string | null>(null);
const dragOverProviderId = ref<string | null>(null);
const dragOverPosition = ref<"before" | "after">("before");

const metadataPlatforms = ref<MetadataPlatformInfo[]>([]);
const testingPlatforms = reactive(new Set<string>());
const testingAllPlatforms = ref(false);
const platformBatchDone = ref(0);
const platformBatchTotal = ref(0);
const platformResults = ref<Record<string, ProbeResult>>({});

const orderedProviders = computed(() => {
  const order = settingsForm.resourceProviderOrder ?? [];
  return [...resourceProviders.value].sort((left, right) => {
    const leftIndex = order.indexOf(left.id);
    const rightIndex = order.indexOf(right.id);
    return (leftIndex === -1 ? order.length : leftIndex) - (rightIndex === -1 ? order.length : rightIndex);
  });
});

function hasCustomMirror(providerId: string): boolean {
  const val = settingsForm.providerMirrors?.[providerId]?.trim();
  if (val) return true;
  if (providerId === "mikan") {
    const legacy = settingsForm.mikanBaseUrl?.trim();
    return Boolean(legacy && legacy !== "https://mikanani.me" && legacy !== "https://mikanani.me/");
  }
  return false;
}

function currentMirrorValue(providerId: string): string {
  const explicit = settingsForm.providerMirrors?.[providerId]?.trim();
  if (explicit) return explicit;
  if (providerId === "mikan") {
    const legacy = settingsForm.mikanBaseUrl?.trim();
    if (legacy && legacy !== "https://mikanani.me") return legacy;
  }
  return "";
}

function toggleMirrorEditor(providerId: string): void {
  if (openMirrorId.value === providerId) {
    openMirrorId.value = null;
    return;
  }
  mirrorDrafts[providerId] = currentMirrorValue(providerId);
  openMirrorId.value = providerId;
}

async function commitMirror(provider: ResourceProviderSummary): Promise<void> {
  const raw = (mirrorDrafts[provider.id] ?? "").trim().replace(/\/+$/u, "");
  const def = (provider.defaultBaseUrl ?? "").trim().replace(/\/+$/u, "");
  if (raw) {
    try {
      const parsed = new URL(raw);
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        throw new Error("invalid protocol");
      }
    } catch {
      toastError(new Error("请输入有效的 http(s):// 镜像地址"), "镜像地址无效");
      return;
    }
  }
  const nextMirrors = { ...(settingsForm.providerMirrors ?? {}) };
  if (!raw || raw === def) {
    delete nextMirrors[provider.id];
    mirrorDrafts[provider.id] = "";
  } else {
    nextMirrors[provider.id] = raw;
  }
  settingsForm.providerMirrors = nextMirrors;
  if (provider.id === "mikan") {
    settingsForm.mikanBaseUrl = nextMirrors.mikan || "https://mikanani.me";
    await saveSettingsFields(["providerMirrors", "mikanBaseUrl"]);
  } else {
    await saveSettingsFields(["providerMirrors"]);
  }
}

async function resetMirror(provider: ResourceProviderSummary): Promise<void> {
  mirrorDrafts[provider.id] = "";
  await commitMirror(provider);
  openMirrorId.value = null;
}

function openProviderDirectProbe(provider: ResourceProviderSummary): void {
  const builtin = BUILTIN_PROBE_MAP[provider.id];
  const mirrorUrl = currentMirrorValue(provider.id);
  if (builtin && !mirrorUrl) {
    openProbe(builtin);
    return;
  }
  const rawUrl = mirrorUrl || provider.defaultBaseUrl || provider.baseUrl || "";
  try {
    const host = new URL(rawUrl).hostname.toLowerCase();
    if (host) {
      openProbe(builtin || `custom-${host}`, host, provider.displayName);
      return;
    }
  } catch {
    // ignore
  }
  if (builtin) {
    openProbe(builtin);
  }
}

function openPlatformDirectProbe(platform: MetadataPlatformInfo): void {
  const builtin = BUILTIN_PROBE_MAP[platform.id];
  if (builtin) {
    openProbe(builtin);
    return;
  }
  const host = PLATFORM_DEFAULT_HOSTS[platform.id];
  if (host) {
    openProbe(`custom-${host}`, host, platform.displayName);
  }
}

function isProviderEnabled(providerId: string): boolean {
  return (settingsForm.enabledResourceProviders ?? []).includes(providerId);
}

function toggleProvider(providerId: string, enabled: boolean): void {
  const current = new Set(settingsForm.enabledResourceProviders ?? []);
  if (enabled) {
    current.add(providerId);
  } else {
    current.delete(providerId);
  }
  settingsForm.enabledResourceProviders = orderedProviders.value
    .map((provider) => provider.id)
    .filter((id) => current.has(id));
}

function moveProvider(providerId: string, delta: number): void {
  const order = orderedProviders.value.map((provider) => provider.id);
  const index = order.indexOf(providerId);
  const target = index + delta;
  if (index === -1 || target < 0 || target >= order.length) {
    return;
  }
  const [moved] = order.splice(index, 1);
  order.splice(target, 0, moved as string);
  settingsForm.resourceProviderOrder = order;
  settingsForm.enabledResourceProviders = order.filter((id) => isProviderEnabled(id));
}

function reorderProvider(fromId: string, toId: string, position: "before" | "after"): void {
  if (fromId === toId) return;
  const order = orderedProviders.value.map((provider) => provider.id);
  const fromIdx = order.indexOf(fromId);
  if (fromIdx === -1) return;
  const [moved] = order.splice(fromIdx, 1);
  let targetIdx = order.indexOf(toId);
  if (targetIdx === -1) return;
  if (position === "after") {
    targetIdx += 1;
  }
  order.splice(targetIdx, 0, moved as string);
  settingsForm.resourceProviderOrder = order;
  settingsForm.enabledResourceProviders = order.filter((id) => isProviderEnabled(id));
}

function handleProviderDragStart(providerId: string, event: DragEvent): void {
  draggingProviderId.value = providerId;
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", providerId);
  }
}

function handleProviderDragOver(providerId: string, event: DragEvent): void {
  if (!draggingProviderId.value || draggingProviderId.value === providerId) {
    dragOverProviderId.value = null;
    return;
  }
  event.preventDefault();
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = "move";
  }
  const el = event.currentTarget as HTMLElement | null;
  if (el) {
    const rect = el.getBoundingClientRect();
    dragOverPosition.value = event.clientY < rect.top + rect.height / 2 ? "before" : "after";
  }
  dragOverProviderId.value = providerId;
}

function handleProviderDrop(providerId: string, event: DragEvent): void {
  event.preventDefault();
  const fromId = draggingProviderId.value || event.dataTransfer?.getData("text/plain") || "";
  if (fromId && fromId !== providerId) {
    reorderProvider(fromId, providerId, dragOverPosition.value);
  }
  clearProviderDrag();
}

function clearProviderDrag(): void {
  draggingProviderId.value = null;
  dragOverProviderId.value = null;
}

function handleProviderHandleKeydown(providerId: string, event: KeyboardEvent): void {
  if (event.key === "ArrowUp") {
    event.preventDefault();
    moveProvider(providerId, -1);
  } else if (event.key === "ArrowDown") {
    event.preventDefault();
    moveProvider(providerId, 1);
  }
}

async function testProvider(providerId: string): Promise<void> {
  if (testingProviders.has(providerId)) return;
  testingProviders.add(providerId);
  try {
    const status = await testResourceProvider(providerId, "1080p");
    providerResults.value = { ...providerResults.value, [providerId]: { ok: status.ok, count: status.count, error: status.error } };
  } catch (error) {
    providerResults.value = {
      ...providerResults.value,
      [providerId]: { ok: false, count: 0, error: error instanceof Error ? error.message : String(error) }
    };
  } finally {
    testingProviders.delete(providerId);
  }
}

async function testAllProviders(): Promise<void> {
  if (testingAllProviders.value || !orderedProviders.value.length) return;
  const ids = orderedProviders.value.map((p) => p.id);
  testingAllProviders.value = true;
  providerBatchDone.value = 0;
  providerBatchTotal.value = ids.length;
  let cursor = 0;
  const concurrency = Math.min(4, ids.length);
  try {
    await Promise.all(
      Array.from({ length: concurrency }, async () => {
        while (cursor < ids.length) {
          const id = ids[cursor++]!;
          await testProvider(id);
          providerBatchDone.value += 1;
        }
      })
    );
  } finally {
    testingAllProviders.value = false;
  }
}

function isPlatformEnabled(platformId: string): boolean {
  return (settingsForm.enabledMetadataPlatforms ?? []).includes(platformId);
}

function toggleMetadataPlatform(platformId: string, enabled: boolean): void {
  const current = new Set(settingsForm.enabledMetadataPlatforms ?? []);
  if (enabled) {
    current.add(platformId);
  } else {
    current.delete(platformId);
  }
  settingsForm.enabledMetadataPlatforms = metadataPlatforms.value
    .map((platform) => platform.id)
    .filter((id) => current.has(id));
  void saveSettingsFields(["enabledMetadataPlatforms"]);
}

function platformIdleLabel(platform: MetadataPlatformInfo): string {
  return platform.requiresApiKey && !platform.configured ? "未填 Key" : "未测试";
}

async function testPlatform(platformId: string): Promise<void> {
  if (testingPlatforms.has(platformId)) return;
  testingPlatforms.add(platformId);
  try {
    if (platformId === "tmdb") {
      await saveSettingsFields(["tmdbApiKey"]);
    }
    const status = await testMetadataPlatform(platformId, "葬送のフリーレン");
    platformResults.value = { ...platformResults.value, [platformId]: { ok: status.ok, count: status.count, error: status.reason || null } };
  } catch (error) {
    platformResults.value = {
      ...platformResults.value,
      [platformId]: { ok: false, count: 0, error: error instanceof Error ? error.message : String(error) }
    };
  } finally {
    testingPlatforms.delete(platformId);
  }
}

async function testAllPlatforms(): Promise<void> {
  if (testingAllPlatforms.value || !metadataPlatforms.value.length) return;
  const ids = metadataPlatforms.value.map((p) => p.id);
  testingAllPlatforms.value = true;
  platformBatchDone.value = 0;
  platformBatchTotal.value = ids.length;
  try {
    await Promise.all(
      ids.map(async (id) => {
        await testPlatform(id);
        platformBatchDone.value += 1;
      })
    );
  } finally {
    testingAllPlatforms.value = false;
  }
}

onMounted(async () => {
  const [providersRes, platformsRes] = await Promise.allSettled([
    listResourceProviders(),
    listMetadataPlatforms()
  ]);
  if (providersRes.status === "fulfilled") {
    resourceProviders.value = providersRes.value;
    for (const provider of providersRes.value) {
      if (hasCustomMirror(provider.id)) {
        mirrorDrafts[provider.id] = currentMirrorValue(provider.id);
      }
    }
  }
  if (platformsRes.status === "fulfilled") {
    metadataPlatforms.value = platformsRes.value;
  }
});
</script>

<template>
  <SettingsSection id="sites" title="种子资源站">
    <div class="settings-group">
      <SettingsItem as="div" stacked>
        <div class="provider-list">
          <div v-if="orderedProviders.length" class="source-list-toolbar">
            <span class="text-xs text-[var(--muted)]">拖动左侧手柄可调整资源站优先级</span>
            <button
              type="button"
              class="settings-secondary-button"
              :disabled="testingAllProviders"
              @click="testAllProviders"
            >
              {{ testingAllProviders ? `测试中 (${providerBatchDone}/${providerBatchTotal})…` : "测试全部" }}
            </button>
          </div>
          <div
            v-for="provider in orderedProviders"
            :key="provider.id"
            class="source-entry"
            :class="{
              'is-dragging': draggingProviderId === provider.id,
              'is-drop-before': dragOverProviderId === provider.id && dragOverPosition === 'before',
              'is-drop-after': dragOverProviderId === provider.id && dragOverPosition === 'after'
            }"
            draggable="true"
            @dragstart="handleProviderDragStart(provider.id, $event)"
            @dragover="handleProviderDragOver(provider.id, $event)"
            @drop="handleProviderDrop(provider.id, $event)"
            @dragend="clearProviderDrag"
          >
            <div class="source-row is-draggable">
              <button
                type="button"
                class="drag-handle"
                :aria-label="`拖动排序 ${provider.displayName}（支持上下方向键）`"
                title="拖动调整优先级（聚焦后也可用 ↑/↓ 键）"
                @keydown="handleProviderHandleKeydown(provider.id, $event)"
              >
                <GripVertical :size="15" aria-hidden="true" />
              </button>
              <input
                :checked="isProviderEnabled(provider.id)"
                type="checkbox"
                class="settings-switch"
                :aria-label="`启用 ${provider.displayName}`"
                @change="toggleProvider(provider.id, ($event.target as HTMLInputElement).checked)"
              />
              <span class="provider-name">{{ provider.displayName }}</span>
              <span class="provider-status" :class="probeStatusClass(providerResults[provider.id])">
                {{ testingProviders.has(provider.id) ? "测试中…" : probeStatusLabel(providerResults[provider.id]) }}
              </span>

              <div class="source-actions">
                <button
                  type="button"
                  class="btn-ghost provider-button"
                  :class="{ 'is-customized': hasCustomMirror(provider.id) }"
                  @click="toggleMirrorEditor(provider.id)"
                >
                  镜像
                </button>
                <button
                  type="button"
                  class="btn-ghost provider-button"
                  @click="openProviderDirectProbe(provider)"
                >
                  直连
                </button>
                <button
                  type="button"
                  class="btn-ghost provider-button"
                  :disabled="testingProviders.has(provider.id)"
                  @click="testProvider(provider.id)"
                >
                  {{ testingProviders.has(provider.id) ? "测试中…" : "测试" }}
                </button>
              </div>
            </div>

            <div v-if="openMirrorId === provider.id || hasCustomMirror(provider.id)" class="source-mirror-bar">
              <input
                v-model="mirrorDrafts[provider.id]"
                class="settings-input flex-1 text-xs"
                spellcheck="false"
                :placeholder="provider.defaultBaseUrl || 'https://...'"
                :aria-label="`${provider.displayName} 镜像地址`"
                @blur="commitMirror(provider)"
                @keydown.enter.prevent.stop="commitMirror(provider)"
              />
              <button
                v-if="hasCustomMirror(provider.id)"
                type="button"
                class="btn-ghost provider-button"
                @click="resetMirror(provider)"
              >
                默认
              </button>
            </div>
          </div>
          <p v-if="!orderedProviders.length" class="settings-desc">加载中…</p>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="metadata" title="番剧信息">
    <div class="settings-group">
      <SettingsItem as="div" stacked>
        <div class="provider-list">
          <div v-if="metadataPlatforms.length" class="source-list-toolbar">
            <span class="text-xs text-[var(--muted)]">共 {{ metadataPlatforms.length }} 个信息平台</span>
            <button
              type="button"
              class="settings-secondary-button"
              :disabled="testingAllPlatforms"
              @click="testAllPlatforms"
            >
              {{ testingAllPlatforms ? `测试中 (${platformBatchDone}/${platformBatchTotal})…` : "测试全部" }}
            </button>
          </div>
          <div v-for="platform in metadataPlatforms" :key="platform.id" class="source-entry">
            <div class="source-row">
              <input
                :checked="isPlatformEnabled(platform.id)"
                type="checkbox"
                class="settings-switch"
                :aria-label="`启用 ${platform.displayName}`"
                @change="toggleMetadataPlatform(platform.id, ($event.target as HTMLInputElement).checked)"
              />
              <span class="provider-name">{{ platform.displayName }}</span>
              <span class="provider-status" :class="probeStatusClass(platformResults[platform.id])">
                {{ testingPlatforms.has(platform.id) ? "测试中…" : probeStatusLabel(platformResults[platform.id], platformIdleLabel(platform)) }}
              </span>

              <div class="source-actions">
                <button
                  type="button"
                  class="btn-ghost provider-button"
                  @click="openPlatformDirectProbe(platform)"
                >
                  直连
                </button>
                <button
                  type="button"
                  class="btn-ghost provider-button"
                  :disabled="testingPlatforms.has(platform.id)"
                  @click="testPlatform(platform.id)"
                >
                  {{ testingPlatforms.has(platform.id) ? "测试中…" : "测试" }}
                </button>
              </div>
            </div>
          </div>
          <p v-if="!metadataPlatforms.length" class="settings-desc">加载中…</p>
        </div>
      </SettingsItem>

      <SettingsItem stacked title="TMDB API Key">
        <input
          v-model="settingsForm.tmdbApiKey"
          class="settings-input settings-input-wide"
          type="password"
          autocomplete="off"
          placeholder="选填"
        />
      </SettingsItem>
    </div>
  </SettingsSection>
</template>

<style scoped>
.source-list-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding-bottom: 0.55rem;
  margin-bottom: 0.15rem;
  border-bottom: 1px solid color-mix(in srgb, var(--line) 70%, transparent);
}

.source-entry {
  position: relative;
  transition: opacity 140ms ease, background 140ms ease;
}

.source-entry + .source-entry {
  border-top: 1px solid color-mix(in srgb, var(--line) 55%, transparent);
}

.source-entry.is-dragging {
  opacity: 0.4;
}

.source-entry.is-drop-before {
  box-shadow: inset 0 2px 0 var(--accent);
}

.source-entry.is-drop-after {
  box-shadow: inset 0 -2px 0 var(--accent);
}

.source-row {
  display: grid;
  grid-template-columns: auto minmax(5.5rem, 1fr) minmax(0, 1.35fr) auto;
  align-items: center;
  gap: 0.5rem;
  padding: 0.375rem 0;
}

.source-row.is-draggable {
  grid-template-columns: auto auto minmax(5.5rem, 1fr) minmax(0, 1.35fr) auto;
}

.drag-handle {
  display: inline-grid;
  place-items: center;
  width: 24px;
  height: 28px;
  border-radius: 6px;
  color: var(--muted);
  cursor: grab;
  transition: background 140ms ease, color 140ms ease;
}

.drag-handle:hover,
.drag-handle:focus-visible {
  background: var(--panel);
  color: var(--ink);
  outline: none;
}

.drag-handle:active {
  cursor: grabbing;
}

.source-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.28rem;
}

.provider-button.is-customized {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.source-mirror-bar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.15rem 0 0.5rem 2.75rem;
}

@media (max-width: 640px) {
  .source-row {
    grid-template-columns: auto minmax(5rem, 1fr) auto;
  }
  .source-row.is-draggable {
    grid-template-columns: auto auto minmax(5rem, 1fr) auto;
  }
  .provider-status {
    display: none;
  }
  .source-mirror-bar {
    padding-left: 0;
  }
}
</style>
