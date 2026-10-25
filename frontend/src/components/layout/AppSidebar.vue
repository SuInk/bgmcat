<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { status } from "../../stores/session";
import AppNavIcon from "./AppNavIcon.vue";
import { activeThemeLabel, setThemeMode, themeMode } from "../../stores/theme";

const props = defineProps<{
 tabs: Array<{ key: string; label: string; hint: string; icon: string }>;
 activeTab: string; collapsed: boolean; mobile?: boolean; mobileOpen?: boolean;
 hasConsoleAccess: boolean; activeDownloadCount: number;
 unreadCompletedDownloadCount?: number; updateAvailable?: boolean;
}>();
const emit = defineEmits<{ (event: "select-tab", tab: string): void; (event: "toggle-collapse"): void }>();
const root = ref<HTMLElement | null>(null);
const hidden = computed(() => props.mobile ? !props.mobileOpen : props.collapsed);
const groups = computed(() => [
 { label: "追番", items: props.tabs.filter(t => ["home", "favorites"].includes(t.key)) },
 { label: "资源管理", items: props.tabs.filter(t => ["subscriptions", "downloads"].includes(t.key)) },
 { label: "工具", items: props.tabs.filter(t => ["logs", "integrations"].includes(t.key)) }
]);
function cycleTheme() { setThemeMode(themeMode.value === "system" ? "light" : themeMode.value === "light" ? "dark" : "system"); }
watch(() => props.collapsed, async collapsed => { if (!props.mobile && !collapsed) { await nextTick(); root.value?.querySelector<HTMLButtonElement>('button')?.focus(); } });
watch(() => props.mobileOpen, async open => { if (props.mobile && open) { await nextTick(); root.value?.querySelector<HTMLButtonElement>('button')?.focus(); } });
function trapFocus(event: KeyboardEvent) {
 if (!props.mobile || !props.mobileOpen || event.key !== "Tab") return;
 const buttons = [...(root.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
 const first = buttons[0], last = buttons[buttons.length - 1];
 if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
 else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
}
</script>

<template>
 <aside id="app-sidebar" ref="root" class="app-sidebar" :class="{ 'sidebar-drawer': mobile, 'sidebar-hidden': hidden }" :inert="hidden" :aria-hidden="hidden" aria-label="主导航" @keydown="trapFocus">
  <div class="sidebar-head">
   <div class="brand"><img src="/icons/logo-96.png" alt="" width="32" height="32" /><div><strong>Bangumi Cat</strong><span>猫追番</span></div></div>
   <button type="button" class="sidebar-toggle" :aria-label="mobile ? '关闭侧栏' : '收起侧栏'" :title="mobile ? '关闭侧栏' : '收起侧栏'" aria-controls="app-sidebar" :aria-expanded="!hidden" @click="emit('toggle-collapse')"><AppNavIcon :name="mobile ? 'close' : 'panel-left'" /></button>
  </div>
  <nav class="nav-sections" aria-label="主导航分类">
   <div v-for="group in groups" :key="group.label" class="nav-section">
    <p class="nav-group-title">{{ group.label }}</p>
    <button v-for="tab in group.items" :key="tab.key" class="nav-item" type="button" :class="{ active: activeTab === tab.key }" :aria-current="activeTab === tab.key ? 'page' : undefined" :title="tab.label" :disabled="!hasConsoleAccess" @click="emit('select-tab', tab.key)">
     <AppNavIcon :name="tab.key" /><span class="nav-label">{{ tab.label }}</span>
     <span v-if="tab.key === 'downloads' && (unreadCompletedDownloadCount ?? 0) > 0" class="nav-count completed" :title="`${unreadCompletedDownloadCount} 个下载已完成`">{{ Math.min(unreadCompletedDownloadCount ?? 0, 99) }}</span>
     <span v-else-if="tab.key === 'downloads' && activeDownloadCount > 0" class="nav-count">{{ Math.min(activeDownloadCount, 99) }}</span>
    </button>
   </div>
  </nav>
  <div class="nav-footer">
   <button class="nav-item" :class="{ active: activeTab === 'settings' }" :aria-current="activeTab === 'settings' ? 'page' : undefined" :disabled="!hasConsoleAccess" @click="emit('select-tab', 'settings')"><AppNavIcon name="settings" /><span class="nav-label">设置</span><span v-if="updateAvailable" class="update-dot" title="有可用更新" /></button>
   <button class="nav-item" :title="`界面主题：${activeThemeLabel}，点击切换`" @click="cycleTheme"><AppNavIcon :name="themeMode === 'dark' ? 'moon' : themeMode === 'light' ? 'sun' : 'theme'" /><span class="nav-label">{{ activeThemeLabel }}</span></button>
   <span class="sidebar-caption">Bangumi Cat<span v-if="status?.appVersion"> · v{{ status.appVersion }}</span></span>
  </div>
 </aside>
</template>

<style scoped>
.app-sidebar { display:flex; flex-direction:column; gap:4px; height:100dvh; width:236px; min-height:0; padding:20px 14px max(20px, env(safe-area-inset-bottom)); background:var(--sidebar-bg); border-right:1px solid var(--line); z-index:40; transition:transform .2s, opacity .2s; }
.sidebar-hidden { transform:translateX(-100%); opacity:0; visibility:hidden; pointer-events:none; }.sidebar-drawer { position:fixed; inset:0 auto 0 0; width:min(82vw,280px); box-shadow:var(--strong-shadow); z-index:50; }
.sidebar-head { display:flex; align-items:center; justify-content:space-between; gap:8px; margin-bottom:24px; padding:0 4px; }.brand { display:flex; align-items:center; gap:10px; min-width:0; }.brand img { border-radius:9px; }.brand strong { display:block; font-size:14px; font-weight:650; }.brand span { display:block; font-size:11px; color:var(--muted); margin-top:2px; }
.sidebar-toggle { display:grid; place-items:center; width:32px; height:32px; border-radius:8px; color:var(--muted); flex-shrink:0; }.sidebar-toggle:hover { background:var(--panel); color:var(--ink); }.sidebar-toggle svg { width:18px; height:18px; }
.nav-sections { display:flex; flex-direction:column; gap:20px; flex:1; min-height:0; overflow:auto; }.nav-section { display:flex; flex-direction:column; gap:2px; }.nav-group-title { padding:0 12px 6px; font-size:11px; font-weight:600; letter-spacing:.08em; color:var(--muted); }
.nav-item { display:flex; align-items:center; gap:10px; width:100%; min-height:38px; padding:9px 12px; border-radius:12px; font-size:13.5px; text-align:left; color:var(--ink); background:transparent; transition:background .15s,color .15s; }.nav-item svg { width:17px; height:17px; flex-shrink:0; opacity:.8; }.nav-item:hover:not(:disabled) { background:var(--panel); }.nav-item.active { background:var(--accent-soft); color:var(--accent); font-weight:600; }.nav-item:disabled { opacity:.45; cursor:not-allowed; }.nav-label { flex:1; min-width:0; }.nav-count { border-radius:6px; padding:1px 5px; background:var(--accent-soft); color:var(--accent); font-size:11px; }.completed { background:var(--ok-soft); color:var(--ok); }
.nav-footer { margin-top:auto; padding-top:12px; border-top:1px solid var(--line); display:grid; gap:4px; }.sidebar-caption { padding:6px 12px 0; color:var(--muted); font-size:11px; }.update-dot { width:7px; height:7px; border-radius:50%; background:var(--warn); }
@media(max-width:960px) { .nav-item { min-height:44px; } }
</style>
