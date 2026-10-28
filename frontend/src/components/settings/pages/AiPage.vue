<script setup lang="ts">
/**
 * 本地 AI（Ollama）。目前用在番剧详情资源列表的「AI 精筛」：把一句话和候选标题交给本机模型挑选。
 * 请求只发到这里填的地址（默认本机 11434），资源标题不会离开这台机器。
 */
import { computed, onMounted, ref } from "vue";

import type { LocalAiStatus } from "@shared/index";

import { getLocalAiStatus } from "../../../api";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { saveSettingsFields, settingsForm } from "../../../stores/session";

const status = ref<LocalAiStatus | null>(null);
const checking = ref(false);

async function checkStatus(): Promise<void> {
  checking.value = true;
  try {
    // 先把刚改的地址 / 模型存下，再按新配置探测
    await saveSettingsFields(["localAiBaseUrl", "localAiModel"]);
    status.value = await getLocalAiStatus();
  } catch {
    status.value = null;
  } finally {
    checking.value = false;
  }
}

const statusLabel = computed(() => {
  if (!settingsForm.localAiEnabled) {
    return "未启用";
  }
  if (!status.value) {
    return "未检测";
  }
  if (status.value.reachable) {
    const hasModel = status.value.availableModels.includes(settingsForm.localAiModel);
    return hasModel
      ? `已连接 · ${status.value.availableModels.length} 个模型`
      : `已连接，但没有「${settingsForm.localAiModel}」，先在终端执行 ollama pull ${settingsForm.localAiModel}`;
  }
  return `连不上：${status.value.lastError ?? "未知错误"}`;
});

onMounted(() => {
  if (settingsForm.localAiEnabled) {
    void checkStatus();
  }
});
</script>

<template>
  <SettingsSection id="ai" title="本地 AI">
    <div class="settings-group">
      <SettingsItem title="启用本地 AI" desc="番剧详情的资源列表里会多一个「AI 精筛」：用一句话描述想要的版本，交给本机模型挑选">
        <input v-model="settingsForm.localAiEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>
      <SettingsItem stacked title="Ollama 地址" desc="装好 Ollama 后默认就是这个地址；资源标题只发到这里，不会上传到别处">
        <input v-model="settingsForm.localAiBaseUrl" class="settings-input settings-input-wide" placeholder="http://127.0.0.1:11434" spellcheck="false" />
      </SettingsItem>
      <SettingsItem stacked title="模型" desc="小模型就够用，例如 gemma3:4b、qwen2.5:7b">
        <input
          v-model="settingsForm.localAiModel"
          class="settings-input settings-input-wide"
          list="local-ai-models"
          placeholder="gemma3:4b"
          spellcheck="false"
        />
        <datalist id="local-ai-models">
          <option v-for="model in status?.availableModels ?? []" :key="model" :value="model" />
        </datalist>
      </SettingsItem>
      <SettingsItem as="div" title="连接状态" :desc="statusLabel">
        <button type="button" class="settings-secondary-button" :disabled="checking || !settingsForm.localAiEnabled" @click="checkStatus">
          {{ checking ? "检测中…" : "检测连接" }}
        </button>
      </SettingsItem>
    </div>
  </SettingsSection>
</template>
