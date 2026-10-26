<script setup lang="ts">
import { computed, inject } from "vue";

import SettingsItem from "../SettingsItem.vue";
import SettingsSection from "../SettingsSection.vue";
import VersionPanel from "../VersionPanel.vue";
import { settingsPageVisibleKey } from "../context";
import { openExternalLink } from "../../../lib/actions";

/** Limited Redistribution License (SuInk) 第 13 条：通过网络向他人提供服务时，要给出获取对应源码的途径 */
const SOURCE_REPOSITORY_URL = "https://github.com/SuInk/bgmcat";

const isPageVisible = inject(settingsPageVisibleKey, () => true);
// 版本面板一挂载就去查更新，只在这一页真的露出来时再挂
const visible = computed(() => isPageVisible("about"));
</script>

<template>
  <SettingsSection id="about" title="版本与更新">
    <VersionPanel v-if="visible" />

    <div class="settings-group">
      <SettingsItem as="div" title="开源协议">
        <template #desc>
          Limited Redistribution License (SuInk)。修改后分发或部署成网络服务时，需要以同样协议公开完整源码。
          Go 服务通过托管 aria2c 下载，内置 Google Drive 接入；可选 FFmpeg、cloudflared 遵循各自许可。
        </template>
        <div class="flex shrink-0 gap-2">
          <button type="button" class="settings-secondary-button" @click="openExternalLink(`${SOURCE_REPOSITORY_URL}/blob/main/LICENSE`)">协议全文</button>
          <button type="button" class="settings-secondary-button" @click="openExternalLink(SOURCE_REPOSITORY_URL)">源码</button>
        </div>
      </SettingsItem>
    </div>
  </SettingsSection>
</template>
