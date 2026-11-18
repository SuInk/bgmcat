<script setup lang="ts">
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
    await saveSettingsFields(["localAiBaseUrl", "localAiApiKey", "localAiModel"]);
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
    const count = status.value.availableModels.length;
    if (!count) {
      return "已连接";
    }
    const hasModel = status.value.availableModels.includes(settingsForm.localAiModel);
    return hasModel
      ? `已连接 · ${count} 个模型`
      : `已连接（未找到 ${settingsForm.localAiModel}）`;
  }
  return `未连通：${status.value.lastError ?? "连接失败"}`;
});

onMounted(() => {
  if (settingsForm.localAiEnabled) {
    void checkStatus();
  }
});
</script>

<template>
  <SettingsSection id="ai" title="AI 精筛">
    <div class="settings-group">
      <SettingsItem title="启用 AI 精筛">
        <input v-model="settingsForm.localAiEnabled" type="checkbox" class="settings-switch" />
      </SettingsItem>
      <SettingsItem stacked title="接口地址">
        <input
          v-model="settingsForm.localAiBaseUrl"
          class="settings-input settings-input-wide"
          placeholder="http://127.0.0.1:11434 或 https://api.openai.com/v1"
          spellcheck="false"
        />
      </SettingsItem>
      <SettingsItem stacked title="API Key">
        <input
          v-model="settingsForm.localAiApiKey"
          type="text"
          class="secret-mask-input settings-input settings-input-wide"
          autocomplete="off"
          placeholder="本地 Ollama 留空；OpenAI 兼容接口填写"
          spellcheck="false"
        />
      </SettingsItem>
      <SettingsItem stacked title="模型">
        <input
          v-model="settingsForm.localAiModel"
          class="settings-input settings-input-wide"
          list="local-ai-models"
          placeholder="gemma3:4b / gpt-4o-mini / deepseek-chat"
          spellcheck="false"
        />
        <datalist id="local-ai-models">
          <option v-for="model in status?.availableModels ?? []" :key="model" :value="model" />
        </datalist>
      </SettingsItem>
      <SettingsItem as="div" title="状态" :desc="statusLabel">
        <button
          type="button"
          class="settings-secondary-button"
          :disabled="checking || !settingsForm.localAiEnabled"
          @click="checkStatus"
        >
          {{ checking ? "检测中…" : "检测" }}
        </button>
      </SettingsItem>
    </div>
  </SettingsSection>
</template>
