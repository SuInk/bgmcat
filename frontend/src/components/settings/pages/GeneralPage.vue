<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { DEFAULT_THEME_ACCENT } from "@shared/index";

import SegmentedControl from "../../common/SegmentedControl.vue";
import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import { saveSettingsFields, settingsFieldErrors, settingsForm } from "../../../stores/session";
import { previewThemeAccent, setThemeMode, themeMode } from "../../../stores/theme";

const THEME_MODE_OPTIONS = [
  { value: "system", label: "跟随系统" },
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" }
] as const;

const ACCENT_PRESETS = [
  { label: "电光蓝", value: "#0041fd" },
  { label: "樱粉", value: "#e0578f" },
  { label: "星夜紫", value: "#7c5cff" },
  { label: "松间绿", value: "#159f6c" }
] as const;

const themeModeModel = computed({ get: () => themeMode.value, set: setThemeMode });

const ACCENT_HEX_RE = /^#[0-9a-fA-F]{6}$/u;

const accentHexDraft = ref(settingsForm.themeAccent);

/**
 * 选色即时预览（只写根元素变量）。
 * commit=false 用在取色器拖动过程中：预览要跟手，但别每动一下就发一次请求。
 */
function pickAccent(value: string, commit = true): void {
  if (!ACCENT_HEX_RE.test(value.trim())) {
    return;
  }
  const next = value.trim().toLowerCase();
  settingsForm.themeAccent = next;
  accentHexDraft.value = next;
  previewThemeAccent(next);
  if (commit) {
    void saveSettingsFields(["themeAccent"]);
  }
}

/** hex 输入框失焦/回车才认；非法就退回当前值，不要边打字边闪。 */
function commitAccentHex(): void {
  const raw = accentHexDraft.value.trim();
  const normalized = raw.startsWith("#") ? raw : `#${raw}`;
  if (!ACCENT_HEX_RE.test(normalized)) {
    accentHexDraft.value = settingsForm.themeAccent;
    return;
  }
  pickAccent(normalized);
}

// 设置从服务端到达（或用户点了「重置」）时，把草稿和预览拉回真身
watch(
  () => settingsForm.themeAccent,
  (next) => {
    if (next && next !== accentHexDraft.value) {
      accentHexDraft.value = next;
    }
    if (next) {
      previewThemeAccent(next);
    }
  }
);
</script>

<template>
  <SettingsSection id="appearance" title="外观">
    <div class="settings-group">
      <!-- 明暗模式 + 主题色。换色立刻生效（只写根元素变量），保存才落库。 -->
      <SettingsItem as="div" stacked desc="明暗模式随时生效；主题色只选一个颜色，悬停色、选中底色、深色模式那版都由它算出来。">
        <div class="appearance-rows">
          <div class="appearance-row">
            <span class="appearance-label">明暗模式</span>
            <SegmentedControl v-model="themeModeModel" :options="THEME_MODE_OPTIONS" aria-label="明暗模式" />
          </div>

          <div class="appearance-row">
            <span class="appearance-label">主题色</span>
            <div class="appearance-accent">
              <div class="appearance-presets">
                <button
                  v-for="preset in ACCENT_PRESETS"
                  :key="preset.value"
                  type="button"
                  :class="['appearance-chip', settingsForm.themeAccent === preset.value ? 'is-on' : '']"
                  :style="{ '--chip-color': preset.value }"
                  @click="pickAccent(preset.value)"
                >
                  <span class="appearance-dot"></span>
                  {{ preset.label }}
                </button>
              </div>
              <div class="appearance-custom">
                <label class="appearance-chip appearance-chip-custom">
                  <span class="appearance-dot appearance-dot-rainbow"></span>
                  自定义
                  <input
                    type="color"
                    class="appearance-color-input"
                    :value="settingsForm.themeAccent"
                    aria-label="自定义主题色"
                    @input="pickAccent(($event.target as HTMLInputElement).value, false)"
                    @change="pickAccent(($event.target as HTMLInputElement).value)"
                  />
                </label>
                <input
                  v-model="accentHexDraft"
                  class="settings-input appearance-hex"
                  spellcheck="false"
                  aria-label="主题色 hex"
                  placeholder="#0041FD"
                  @blur="commitAccentHex"
                  @keydown.enter.prevent="commitAccentHex"
                />
                <button type="button" class="appearance-reset" @click="pickAccent(DEFAULT_THEME_ACCENT)">恢复默认</button>
              </div>
            </div>
          </div>

          <div class="appearance-row">
            <span class="appearance-label">预览</span>
            <div class="appearance-preview">
              <span class="appearance-preview-primary">主操作</span>
              <span class="appearance-preview-ghost">次操作</span>
              <span class="appearance-preview-selected">选中项</span>
              <span class="appearance-preview-bar"><span :style="{ width: '62%' }"></span></span>
            </div>
          </div>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="run" title="运行">
    <div class="settings-group">
      <SettingsItem title="轮询间隔" desc="多久检查一次订阅更新与下载状态，单位分钟">
        <span class="settings-field">
          <input
            v-model.number="settingsForm.pollIntervalMinutes"
            type="number"
            class="settings-input settings-input-short"
            :class="{ 'settings-input-invalid': settingsFieldErrors.pollIntervalMinutes }"
          />
          <span v-if="settingsFieldErrors.pollIntervalMinutes" class="settings-field-error">{{ settingsFieldErrors.pollIntervalMinutes }}</span>
        </span>
      </SettingsItem>
    </div>
  </SettingsSection>

  <SettingsSection id="service" title="Go 服务">
    <p class="settings-desc">关闭浏览器不会停止下载。服务随 Go 进程运行；开机自启可通过 Docker restart 策略或系统服务配置。</p>
  </SettingsSection>
</template>
