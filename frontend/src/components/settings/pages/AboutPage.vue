<script setup lang="ts">
import { computed, inject } from "vue";

import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import VersionPanel from "../VersionPanel.vue";
import { settingsPageVisibleKey } from "../context";
import { openExternalLink } from "../../../lib/actions";

const SOURCE_REPOSITORY_URL = "https://github.com/SuInk/bgmcat";

const isPageVisible = inject(settingsPageVisibleKey, () => true);
// 版本面板一挂载就去查更新，只在这一页真的露出来时再挂
const visible = computed(() => isPageVisible("about"));
</script>

<template>
  <SettingsSection id="about" title="版本与更新">
    <VersionPanel v-if="visible" />

    <div class="settings-group">
      <SettingsItem as="div" title="软件许可" desc="Limited Redistribution License (SuInk)">
        <div class="flex shrink-0 gap-2">
          <button type="button" class="settings-secondary-button" @click="openExternalLink(`${SOURCE_REPOSITORY_URL}/blob/main/LICENSE`)">许可</button>
          <button type="button" class="settings-secondary-button" @click="openExternalLink(SOURCE_REPOSITORY_URL)">GitHub</button>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>
</template>
