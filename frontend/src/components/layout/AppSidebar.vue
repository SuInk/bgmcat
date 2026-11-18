<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { status } from "../../stores/session";
import AppNavIcon from "./AppNavIcon.vue";
import { setThemeMode, themeMode } from "../../stores/theme";

const props = defineProps<{
 tabs: Array<{ key: string; label: string; hint: string; icon: string }>;
 activeTab: string; collapsed: boolean; mobile?: boolean; mobileOpen?: boolean;
 hasConsoleAccess: boolean; activeDownloadCount: number;
 unreadCompletedDownloadCount?: number; updateAvailable?: boolean;
}>();
const emit = defineEmits<{ (event: "select-tab", tab: string): void; (event: "toggle-collapse"): void; (event: "open-version"): void }>();
const root = ref<HTMLElement | null>(null);
const hidden = computed(() => props.mobile ? !props.mobileOpen : props.collapsed);
const groups = computed(() => [
 { label: "追番", items: props.tabs.filter(t => ["home", "manga", "favorites", "history"].includes(t.key)) },
 { label: "资源管理", items: props.tabs.filter(t => ["subscriptions", "downloads", "files"].includes(t.key)) },
 { label: "工具", items: props.tabs.filter(t => ["logs", "integrations", "settings"].includes(t.key)) }
]);
const themeLabel = computed(() => themeMode.value === "system" ? "跟随系统" : themeMode.value === "light" ? "浅色" : "深色");
const versionLabel = computed(() => status.value?.appVersion ? `v${status.value.appVersion.replace(/^v/, "")}` : "版本与更新");
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
    <button v-for="tab in group.items" :key="tab.key" class="nav-item" type="button" :class="{ active: activeTab === tab.key }" :aria-current="activeTab === tab.key ? 'page' : undefined" :title="collapsed ? tab.label : undefined" :disabled="!hasConsoleAccess" @click="emit('select-tab', tab.key)">
     <AppNavIcon :name="tab.key" /><span class="nav-label">{{ tab.label }}</span>
     <span v-if="tab.key === 'downloads' && (unreadCompletedDownloadCount ?? 0) > 0" class="nav-count completed" :title="`${unreadCompletedDownloadCount} 个下载已完成`">{{ Math.min(unreadCompletedDownloadCount ?? 0, 99) }}</span>
     <span v-else-if="tab.key === 'downloads' && activeDownloadCount > 0" class="nav-count">{{ Math.min(activeDownloadCount, 99) }}</span>
    </button>
   </div>
  </nav>
  <div class="nav-footer">
   <button type="button" class="nav-action" :title="`外观：${themeLabel}（点击切换）`" @click="cycleTheme"><AppNavIcon :name="themeMode === 'dark' ? 'moon' : themeMode === 'light' ? 'sun' : 'theme'" /><span class="nav-label">{{ themeLabel }}</span></button>
   <button type="button" class="nav-action version-action" title="查看版本与更新" :disabled="!hasConsoleAccess" @click="emit('open-version')"><AppNavIcon name="tag" /><span class="version-label-text">{{ versionLabel }}</span><span v-if="updateAvailable" class="update-dot" aria-label="有新版本" /></button>
  </div>
 </aside>
</template>

<style scoped>
/* 尺寸、字号对齐 Diana 控制台（frontend/src/styles/main.css 的 .app-sidebar / .nav-*） */
.app-sidebar { display:flex; flex-direction:column; gap:4px; height:100dvh; width:236px; min-height:0; padding:20px 14px max(20px, env(safe-area-inset-bottom)); background:var(--sidebar-bg); border-right:1px solid var(--line); z-index:40; transition:transform .2s, opacity .2s; }
.sidebar-hidden { transform:translateX(-100%); opacity:0; visibility:hidden; pointer-events:none; }.sidebar-drawer { position:fixed; inset:0 auto 0 0; width:min(82vw,280px); box-shadow:var(--strong-shadow); z-index:50; }
/* 头部跟顶栏等高、抵掉侧栏上内边距，logo 和收起按钮就跟顶栏标题同一条中线 */
.sidebar-head { display:flex; flex:none; align-items:center; justify-content:space-between; gap:8px; height:56px; margin:-20px 0 12px; padding:0 4px 0 8px; }.brand { display:flex; align-items:center; gap:10px; min-width:0; }.brand img { border-radius:11px; }.brand strong { display:block; font-size:14px; font-weight:650; line-height:1.3; }.brand span { display:block; font-size:11px; color:var(--muted); line-height:1.3; }
.sidebar-toggle { display:grid; place-items:center; width:32px; height:32px; border-radius:10px; color:var(--ink-secondary); flex-shrink:0; transition:background .15s, color .15s; }.sidebar-toggle:hover { background:var(--panel); color:var(--ink); }.sidebar-toggle svg { width:18px; height:18px; }
.nav-sections { display:flex; flex-direction:column; gap:14px; flex:1 1 auto; min-height:0; overflow-y:auto; }.nav-section { display:flex; flex-direction:column; gap:2px; }.nav-group-title { padding:0 12px 4px; font-size:11px; font-weight:600; letter-spacing:.08em; color:var(--muted); }
.nav-item { display:flex; align-items:center; gap:10px; width:100%; padding:9px 12px; border-radius:12px; font-size:14px; text-align:left; color:var(--ink-secondary); background:transparent; transition:background .15s,color .15s; }.nav-item svg { width:17px; height:17px; flex-shrink:0; }.nav-item:hover:not(:disabled) { background:var(--panel); color:var(--ink); }.nav-item.active { background:var(--accent-soft); color:var(--accent); font-weight:600; }.nav-item:disabled { opacity:.45; cursor:not-allowed; }.nav-label { flex:1; min-width:0; }.nav-count { display:inline-flex; align-items:center; justify-content:center; min-width:20px; height:20px; border-radius:999px; padding:0 6px; background:var(--accent-soft); color:var(--accent); font-size:11px; font-weight:600; font-variant-numeric:tabular-nums; }.completed { background:var(--ok-soft); color:var(--ok); }
.nav-footer { margin-top:auto; padding:12px 8px 0; border-top:1px solid var(--line); display:flex; flex-direction:column; gap:8px; }.update-dot { width:6px; height:6px; border-radius:50%; background:var(--warn); flex:none; }
.nav-action { display:flex; align-items:center; gap:10px; width:100%; padding:9px 12px; border-radius:12px; background:transparent; color:var(--ink-secondary); font-size:13px; text-align:left; transition:background .15s,color .15s; }
.nav-action svg { width:16px; height:16px; flex-shrink:0; }.nav-action:hover:not(:disabled) { background:var(--panel); color:var(--ink); }.nav-action.active { background:var(--accent-soft); color:var(--accent); font-weight:600; }.nav-action.version-action { padding-inline:10px; font-family:ui-monospace,monospace; font-size:11px; color:var(--muted); }.nav-action.version-action:hover { color:var(--accent); }.version-label-text { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; flex:1; }.nav-action:disabled { opacity:.5; cursor:not-allowed; }
</style>
