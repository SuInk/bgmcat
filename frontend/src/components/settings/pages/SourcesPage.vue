<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import type { ResourceProviderSummary } from "@shared/index";

import { listResourceProviders, testResourceProvider } from "../../../api";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { probeStatusClass, probeStatusLabel, type ProbeResult } from "../probe";
import { settingsForm } from "../../../stores/session";

const resourceProviders = ref<ResourceProviderSummary[]>([]);
const providerTesting = ref<string | null>(null);
const providerResults = ref<Record<string, ProbeResult>>({});

/** 按设置里的顺序排；不在顺序列表里的排最后（新增的源不会凭空跑到最前面）。 */
const orderedProviders = computed(() => {
  const order = settingsForm.resourceProviderOrder ?? [];
  return [...resourceProviders.value].sort((left, right) => {
    const leftIndex = order.indexOf(left.id);
    const rightIndex = order.indexOf(right.id);
    return (leftIndex === -1 ? order.length : leftIndex) - (rightIndex === -1 ? order.length : rightIndex);
  });
});

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
  // 按当前顺序写回，免得开关一次就把顺序打乱
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

async function testProvider(providerId: string): Promise<void> {
  providerTesting.value = providerId;
  try {
    // 用一个大概率有结果的关键词，测的是「连得上且解析得出条目」而不只是 HTTP 200
    const status = await testResourceProvider(providerId, "anime");
    providerResults.value = { ...providerResults.value, [providerId]: { ok: status.ok, count: status.count, error: status.error } };
  } catch (error) {
    providerResults.value = {
      ...providerResults.value,
      [providerId]: { ok: false, count: 0, error: error instanceof Error ? error.message : String(error) }
    };
  } finally {
    providerTesting.value = null;
  }
}

onMounted(async () => {
  try {
    resourceProviders.value = await listResourceProviders();
  } catch {
    // 拉不到就让列表空着显示「正在加载」，不弹错误打断用户改别的设置
  }
});
</script>

<template>
  <SettingsSection id="sites" title="资源站" desc="种子从哪儿找。详情页和订阅都会跨这些站合并，越靠上越优先。">
    <div class="settings-group">
      <!--
        番剧资源的搜索与下载可以同时用多个站点。这里列出全部来源：
        开关决定详情页查不查它、订阅时挂不挂它；顺序决定跨源合并时用谁的标题/链接展示，
        也是同集择优里「来源」这一档的先后。
      -->
      <SettingsItem as="div" stacked>
        <div class="provider-list">
          <div v-for="(provider, index) in orderedProviders" :key="provider.id" class="provider-row">
            <input
              :checked="isProviderEnabled(provider.id)"
              type="checkbox"
              class="settings-switch"
              :aria-label="`启用 ${provider.displayName}`"
              @change="toggleProvider(provider.id, ($event.target as HTMLInputElement).checked)"
            />
            <span class="provider-name">{{ provider.displayName }}</span>
            <span class="provider-status" :class="probeStatusClass(providerResults[provider.id])">
              {{ probeStatusLabel(providerResults[provider.id]) }}
            </span>
            <button type="button" class="btn-ghost provider-button" :disabled="providerTesting === provider.id" @click="testProvider(provider.id)">
              {{ providerTesting === provider.id ? "测试中…" : "测试" }}
            </button>
            <button type="button" class="btn-ghost provider-button" :disabled="index === 0" aria-label="上移" @click="moveProvider(provider.id, -1)">↑</button>
            <button
              type="button"
              class="btn-ghost provider-button"
              :disabled="index === orderedProviders.length - 1"
              aria-label="下移"
              @click="moveProvider(provider.id, 1)"
            >
              ↓
            </button>
          </div>
          <p v-if="!orderedProviders.length" class="settings-desc">正在加载资源来源…</p>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="mikan" title="蜜柑计划">
    <div class="settings-group">
      <SettingsItem stacked title="蜜柑计划地址" desc="默认 mikanani.me。旧镜像 mikanime.tv 现在只会跳转到 mikanani.me，填它会被直接当作 mikanani.me">
        <input v-model="settingsForm.mikanBaseUrl" class="settings-input settings-input-wide" placeholder="https://mikanani.me" />
      </SettingsItem>
    </div>
  </SettingsSection>
</template>
