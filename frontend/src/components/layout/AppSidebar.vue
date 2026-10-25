<script setup lang="ts">
import AppNavIcon from "./AppNavIcon.vue";

defineProps<{
  tabs: Array<{ key: string; label: string; hint: string; icon: string }>;
  activeTab: string;
  collapsed: boolean;
  mobile?: boolean;
  mobileOpen?: boolean;
  hasConsoleAccess: boolean;
  activeDownloadCount: number;
  /** 已完成但未查看的下载数；优先于活跃数显示，用绿色区分 */
  unreadCompletedDownloadCount?: number;
  /** 有可用更新：设置项挂一个小黄点。只提示，装不装由用户在设置页决定。 */
  updateAvailable?: boolean;
}>();

const emit = defineEmits<{
  (event: "select-tab", tab: string): void;
  (event: "toggle-collapse"): void;
}>();
</script>

<template>
  <aside
    class="grid min-h-0 grid-rows-[auto_1fr] border-r border-[var(--line)] transition-transform duration-200 ease-out"
    :class="[
      mobile
        ? [
            'fixed inset-y-0 left-0 z-50 w-[min(82vw,280px)] bg-[var(--sidebar-bg)] px-3.5 pb-4 pt-5',
            mobileOpen ? 'translate-x-0' : '-translate-x-full'
          ]
        : [
            collapsed ? 'w-[88px] px-2' : 'w-[236px] px-3.5',
            'h-screen bg-[var(--sidebar-bg)] pb-4 pt-5'
          ]
    ]"
  >
    <!-- 品牌区(参照 qq-bot:标志 + 名称/副标题) -->
    <div class="mb-4 flex items-center" :class="mobile || !collapsed ? 'justify-between gap-2.5 px-1.5' : 'justify-center'">
      <div class="flex min-w-0 items-center gap-2.5">
        <img src="/icons/logo-96.png" alt="" width="36" height="36" class="size-9 shrink-0 rounded-[11px] object-cover shadow-[var(--soft-shadow)]" />
        <div v-if="mobile || !collapsed" class="min-w-0 leading-tight">
          <div class="truncate text-sm font-semibold tracking-[0.02em] text-[var(--ink)]">猫追番 · Bangumi Cat</div>
          <div class="truncate text-[11px] text-[var(--muted)]">动漫订阅中心</div>
        </div>
      </div>
      <button
        v-if="mobile"
        type="button"
        class="grid size-8 shrink-0 place-items-center rounded-lg text-[var(--muted)] transition hover:bg-[var(--panel)] hover:text-[var(--ink)]"
        aria-label="关闭侧栏"
        title="关闭侧栏"
        @click="emit('toggle-collapse')"
      >
        <AppNavIcon name="close" class="size-4" />
      </button>
    </div>

    <nav class="grid content-start gap-1 overflow-auto">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        type="button"
        class="nav-item"
        :class="[activeTab === tab.key ? 'nav-item-active' : '', mobile || !collapsed ? '' : 'nav-item-rail']"
        :disabled="!hasConsoleAccess"
        @click="emit('select-tab', tab.key)"
      >
        <span class="relative shrink-0">
          <AppNavIcon :name="tab.key" class="size-5" />
          <!-- 完成未读优先：下载完了却没人看，比「还有几个在跑」更值得提示 -->
          <span
            v-if="tab.key === 'downloads' && (unreadCompletedDownloadCount ?? 0) > 0"
            class="absolute -right-2.5 -top-1.5 min-w-4 rounded-full bg-[var(--ok)] px-1 py-px text-center text-[10px] font-semibold leading-[1.2] text-white"
            :title="`${unreadCompletedDownloadCount} 个下载已完成`"
          >
            {{ (unreadCompletedDownloadCount ?? 0) > 99 ? "99+" : unreadCompletedDownloadCount }}
          </span>
          <span
            v-else-if="tab.key === 'downloads' && activeDownloadCount > 0"
            class="absolute -right-2.5 -top-1.5 min-w-4 rounded-full bg-[var(--accent)] px-1 py-px text-center text-[10px] font-semibold leading-[1.2] text-[var(--accent-contrast)]"
          >
            {{ activeDownloadCount > 99 ? "99+" : activeDownloadCount }}
          </span>
          <!-- 有新版本只点一个小黄点：更新是「可以做」，不是「必须做」，不该用数字角标施压 -->
          <span
            v-else-if="tab.key === 'settings' && updateAvailable"
            class="absolute -right-1 -top-0.5 size-2 rounded-full bg-[var(--warn)]"
            title="有可用更新"
          ></span>
        </span>
        <span class="min-w-0 truncate text-left" :class="mobile || !collapsed ? 'flex-1' : 'nav-rail-label'">{{ tab.label }}</span>
      </button>
    </nav>
  </aside>
</template>

<style scoped>
/* 导航项对齐参照项目 .nav-item */
.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 12px;
  border: none;
  border-radius: 12px;
  background: transparent;
  color: var(--muted);
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}

.nav-item:hover:not(:disabled) {
  background: var(--panel);
  color: var(--ink);
}

.nav-item:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.nav-item-active,
.nav-item-active:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: var(--accent-strong);
  font-weight: 600;
}

:global(:root[data-theme="dark"]) .nav-item-active,
:global(:root[data-theme="dark"]) .nav-item-active:hover:not(:disabled) {
  color: var(--accent);
}

.nav-item-rail {
  flex-direction: column;
  justify-content: center;
  gap: 4px;
  padding: 8px 2px;
}

.nav-rail-label {
  font-size: 11px;
  font-weight: 500;
  line-height: 1.2;
}
</style>
