<script setup lang="ts">
import { onMounted, ref } from "vue";

import { listMetadataPlatforms, testMetadataPlatform } from "../../../api";
import type { MetadataPlatformInfo } from "../../../api";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { probeStatusClass, probeStatusLabel, type ProbeResult } from "../probe";
import { settingsForm } from "../../../stores/session";

// 管的是「这部番叫什么、评分多少、简介和封面」，和资源站（种子在哪儿）是两条独立链路。
const metadataPlatforms = ref<MetadataPlatformInfo[]>([]);
const platformTesting = ref<string | null>(null);
const platformResults = ref<Record<string, ProbeResult>>({});

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
  // 按后端给的固定顺序写回，保证聚合结果的排序稳定
  settingsForm.enabledMetadataPlatforms = metadataPlatforms.value
    .map((platform) => platform.id)
    .filter((id) => current.has(id));
}

function platformIdleLabel(platform: MetadataPlatformInfo): string {
  return platform.requiresApiKey && !platform.configured ? "未填写 API key" : "未测试";
}

async function testPlatform(platformId: string): Promise<void> {
  platformTesting.value = platformId;
  try {
    // 用一部几乎必然存在的番做探针：测的是「连得上且解析得出条目」，而不只是 HTTP 200
    const status = await testMetadataPlatform(platformId, "葬送のフリーレン");
    platformResults.value = { ...platformResults.value, [platformId]: { ok: status.ok, count: status.count, error: status.reason || null } };
  } catch (error) {
    platformResults.value = {
      ...platformResults.value,
      [platformId]: { ok: false, count: 0, error: error instanceof Error ? error.message : String(error) }
    };
  } finally {
    platformTesting.value = null;
  }
}

onMounted(async () => {
  try {
    metadataPlatforms.value = await listMetadataPlatforms();
  } catch {
    // 拉不到就让列表空着显示「正在加载」，不打断用户改别的设置
  }
});
</script>

<template>
  <SettingsSection id="metadata" title="番剧信息" desc="番名、评分、简介和封面从哪儿来；各平台评分分别列出，不做平均。">
    <div class="settings-group">
      <!--
        信息平台管的是番剧本身的信息（标题/评分/简介/封面），不是种子。
        多开几个平台能搜到本地库里没有的番；某个平台不通时结果照出，只少它那一份。
      -->
      <SettingsItem as="div" stacked>
        <div class="provider-list">
          <div v-for="platform in metadataPlatforms" :key="platform.id" class="provider-row provider-row-unordered">
            <input
              :checked="isPlatformEnabled(platform.id)"
              type="checkbox"
              class="settings-switch"
              :aria-label="`启用 ${platform.displayName}`"
              @change="toggleMetadataPlatform(platform.id, ($event.target as HTMLInputElement).checked)"
            />
            <span class="provider-name">{{ platform.displayName }}</span>
            <span class="provider-status" :class="probeStatusClass(platformResults[platform.id])">
              {{ probeStatusLabel(platformResults[platform.id], platformIdleLabel(platform)) }}
            </span>
            <button type="button" class="btn-ghost provider-button" :disabled="platformTesting === platform.id" @click="testPlatform(platform.id)">
              {{ platformTesting === platform.id ? "测试中…" : "测试" }}
            </button>
          </div>
          <p v-if="!metadataPlatforms.length" class="settings-desc">正在加载信息平台…</p>
        </div>
      </SettingsItem>

      <SettingsItem
        stacked
        title="TMDB API key"
        desc="留空就不请求 TMDB。填好后点上面 TMDB 那行的「测试」确认。本产品使用 TMDB API，但未经 TMDB 认可或认证。"
      >
        <input v-model="settingsForm.tmdbApiKey" class="settings-input settings-input-wide" type="password" autocomplete="off" placeholder="留空则不启用 TMDB" />
      </SettingsItem>
    </div>
  </SettingsSection>
</template>
