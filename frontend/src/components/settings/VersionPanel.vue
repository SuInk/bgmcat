<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { isDesktopShell, status } from "../../stores/session";
import { updateVersion, updateStatus, updateStatusLabel, updateStatusTone, updateReleases, updateBusy, refreshUpdateOverview, runUpdateCheck } from "../../stores/update";
import { copyText, openExternalLink } from "../../lib/actions";
import AppSelect from "../common/AppSelect.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";

type DeploymentMode = "server" | "docker" | "desktop" | "source";

const DEPLOYMENT_OPTIONS: Array<{ value: DeploymentMode; label: string; hint: string }> = [
  { value: "server", label: "一键脚本", hint: "保留原安装与数据目录，执行前请先停止当前服务。" },
  { value: "docker", label: "Docker", hint: "在 docker-compose.yml 所在目录执行，自动拉取最新镜像并重启。" },
  { value: "desktop", label: "桌面安装包", hint: "退出应用后下载对应平台安装包覆盖安装，数据目录保持不变。" },
  { value: "source", label: "源码", hint: "在项目仓库目录执行，拉取最新代码并重新构建。" },
];

const loading = ref(true);
const deployment = ref<DeploymentMode>(isDesktopShell ? "desktop" : "server");
const copied = ref(false);
const copyError = ref("");
let copyTimer: number | null = null;

const versionLabel = computed(() => `v${(updateVersion.value?.version || status.value?.appVersion || "未知").replace(/^v/, "")}`);
const phaseLabel = computed(() => updateBusy.value ? "检查中…" : updateStatus.value?.kind === "error" ? "检查失败" : updateStatusLabel.value);
const windows = computed(() => updateVersion.value?.platform === "windows");
const activeDeployment = computed(() => DEPLOYMENT_OPTIONS.find((item) => item.value === deployment.value) ?? DEPLOYMENT_OPTIONS[0]);
const installCommand = computed(() =>
  deployment.value === "source"
    ? "git pull --ff-only && sh scripts/build.sh"
    : deployment.value === "docker"
      ? "docker compose pull && docker compose up -d"
      : windows.value
        ? "irm https://raw.githubusercontent.com/SuInk/bgmcat/main/scripts/install.ps1 | iex"
        : "curl -fsSL https://raw.githubusercontent.com/SuInk/bgmcat/main/scripts/install.sh | sh -s -- --start"
);

function date(value: string | null | undefined) { return value ? new Date(value).toLocaleString() : ""; }

function clearCopyTimer() {
  if (copyTimer !== null) {
    window.clearTimeout(copyTimer);
    copyTimer = null;
  }
}

function selectDeployment(mode: DeploymentMode) {
  deployment.value = mode;
  copied.value = false;
  copyError.value = "";
  clearCopyTimer();
}

async function copyCommand() {
  copyError.value = "";
  clearCopyTimer();
  try {
    await copyText(installCommand.value);
    copied.value = true;
    copyTimer = window.setTimeout(() => {
      copied.value = false;
      copyTimer = null;
    }, 2000);
  } catch {
    copyError.value = "复制失败，请手动选中命令复制。";
  }
}

onMounted(async () => { try { await refreshUpdateOverview(); } finally { loading.value = false; } });
onBeforeUnmount(clearCopyTimer);
</script>

<template>
 <div class="version-panel" :aria-busy="loading || updateBusy">
  <header class="version-hero">
   <div class="version-heading">
    <strong class="version-current">{{ versionLabel }}</strong>
    <template v-if="updateStatus?.updateAvailable && updateStatus.latestVersion">
     <span class="version-arrow" aria-hidden="true">→</span>
     <strong class="version-latest">{{ updateStatus.latestVersion }}</strong>
    </template>
    <span class="version-badge" :class="`tone-${updateStatusTone}`" role="status">{{ loading ? '读取中…' : phaseLabel }}</span>
   </div>
   <div class="version-meta">
    <span>{{ updateVersion ? `${updateVersion.platform} / ${updateVersion.arch}` : '正在读取运行平台' }}</span>
    <span v-if="updateStatus?.checkedAt">{{ date(updateStatus.checkedAt) }} 检查过</span>
    <span v-if="updateStatus?.latestPublishedAt">发布于 {{ date(updateStatus.latestPublishedAt) }}</span>
   </div>
   <p v-if="updateStatus?.lastError" class="version-error" role="alert">{{ updateStatus.lastError }}</p>
   <div v-if="loading || updateBusy" class="scope-progress" role="progressbar" aria-label="正在检查版本"><span /></div>
  </header>

  <div class="version-options">
   <div class="option-field">
    <label>更新通道</label>
    <AppSelect size="sm" class="option-select" model-value="stable" disabled aria-label="更新通道" :options="[{ value: 'stable', label: '稳定版' }]" />
   </div>
   <div class="option-field">
    <label>下载来源</label>
    <AppSelect size="sm" class="option-select" model-value="github" disabled aria-label="下载来源" :options="[{ value: 'github', label: 'GitHub 官方仓库' }]" />
   </div>
  </div>

  <div class="version-actions">
   <span class="version-note">{{ updateStatus?.supported ? '' : '自动安装暂不可用' }}</span>
   <button class="version-button" :disabled="loading || updateBusy" @click="runUpdateCheck"><AppNavIcon name="refresh" :class="{ 'animate-spin': updateBusy }" />{{ updateBusy ? '检查中…' : '检查更新' }}</button>
   <button class="version-button" @click="openExternalLink('https://github.com/SuInk/bgmcat/releases')"><AppNavIcon name="external" />查看发布版本</button>
  </div>

  <details class="upgrade-disclosure">
   <summary class="upgrade-summary">
    <span>手动升级</span>
    <span class="upgrade-summary-right">
     <span class="upgrade-summary-hint">{{ activeDeployment.label }}</span>
     <AppNavIcon name="chevron-down" class="upgrade-chevron" />
    </span>
   </summary>
   <div class="upgrade-body">
    <div class="deployment-tabs" role="tablist" aria-label="安装方式">
     <button
      v-for="item in DEPLOYMENT_OPTIONS"
      :key="item.value"
      type="button"
      role="tab"
      class="deployment-tab"
      :class="{ active: deployment === item.value }"
      :aria-selected="deployment === item.value"
      @click="selectDeployment(item.value)"
     >
      {{ item.label }}
     </button>
    </div>

    <div v-if="deployment === 'desktop'" class="desktop-upgrade-row">
     <p class="upgrade-hint">{{ activeDeployment.hint }}</p>
     <button type="button" class="version-button primary" @click="openExternalLink('https://github.com/SuInk/bgmcat/releases')">
      <AppNavIcon name="download" />下载安装包
     </button>
    </div>

    <template v-else>
     <div class="cmd-box">
      <div class="cmd-scroll" title="点击右侧按钮复制命令">
       <span class="cmd-prompt" aria-hidden="true">$</span>
       <code>{{ installCommand }}</code>
      </div>
      <button
       type="button"
       class="cmd-copy-btn"
       :class="{ copied }"
       :aria-label="copied ? '已复制命令' : '复制升级命令'"
       @click="copyCommand"
      >
       <AppNavIcon :name="copied ? 'check' : 'copy'" />
       <span>{{ copied ? '已复制' : '复制' }}</span>
      </button>
     </div>
     <p v-if="copyError" class="version-error">{{ copyError }}</p>
     <p v-else class="upgrade-hint">{{ activeDeployment.hint }}</p>
    </template>
   </div>
  </details>

  <section class="version-history">
   <div class="history-heading">
    <h3>版本历史</h3>
    <button class="version-link" @click="openExternalLink('https://github.com/SuInk/bgmcat/releases')">在 GitHub 查看全部 ↗</button>
   </div>
   <p v-if="!updateReleases.length" class="version-note">{{ updateStatus?.kind === 'no_release' ? '当前没有可用的稳定发布版本。' : '暂无版本记录，点击检查更新获取。' }}</p>
   <details v-for="release in updateReleases" :key="release.tag" class="release-item"><summary><code>{{ release.tag }}</code><span v-if="release.prerelease" class="version-badge">预发布</span><time>{{ date(release.publishedAt) }}</time></summary><pre>{{ release.notes || '此版本未提供更新说明。' }}</pre></details>
  </section>
 </div>
</template>

<style scoped>
.version-panel { display:grid; gap:12px; color:var(--ink); font-size:13px; min-width:0; }
.version-hero { position:relative; overflow:hidden; display:flex; flex-direction:column; gap:6px; padding:13px 15px; border:1px solid var(--line); border-radius:10px; background:var(--card); }
.version-heading { display:flex; align-items:center; gap:9px; flex-wrap:wrap; }
.version-current,.version-latest { font-family:ui-monospace,monospace; font-size:19px; font-weight:650; line-height:1.2; }
.version-latest { color:var(--accent); }
.version-arrow { color:var(--muted); }
.version-badge { padding:2px 9px; border-radius:999px; font-size:11px; font-weight:600; background:var(--panel); color:var(--muted); }
.tone-ok { color:var(--ok); background:var(--ok-soft); }
.tone-err { color:var(--err); background:var(--err-soft); }
.tone-accent { color:var(--accent); background:var(--accent-soft); }
.tone-warn { color:var(--warn); background:var(--warn-soft); }
.version-meta { display:flex; flex-wrap:wrap; gap:4px 14px; color:var(--muted); font-size:12px; }
.version-note { color:var(--muted); font-size:12px; line-height:1.55; margin:0; }
.version-error { color:var(--err); font-size:12px; line-height:1.55; margin:0; overflow-wrap:anywhere; }
.version-options { display:grid; grid-template-columns:repeat(auto-fit, minmax(220px, 1fr)); gap:10px 20px; }
.option-field { display:flex; align-items:center; gap:8px; font-size:12.5px; color:var(--muted); }
.option-field > label { white-space:nowrap; }
.option-select { flex:1 1 auto; min-width:0; }
.version-actions { display:flex; flex-wrap:wrap; gap:8px; align-items:center; }
.version-actions > span { margin-right:auto; }
.version-button { display:inline-flex; justify-content:center; align-items:center; gap:6px; height:32px; border:1px solid var(--line); border-radius:8px; padding:0 12px; background:var(--card); color:var(--ink); font-size:12px; font-weight:550; white-space:nowrap; transition:background .15s, border-color .15s; }
.version-button svg { width:13.5px; height:13.5px; }
.version-button:disabled { opacity:.5; cursor:wait; }
.version-button:hover:not(:disabled) { border-color:var(--accent); background:var(--panel); }
.version-button.primary { border-color:var(--accent); color:var(--accent-contrast); background:var(--accent); }
.upgrade-disclosure { border-top:1px solid var(--line); padding-top:10px; }
.upgrade-summary { display:flex; align-items:center; justify-content:space-between; gap:8px; cursor:pointer; font-size:13px; font-weight:600; color:var(--ink-secondary); list-style:none; padding:2px 0; user-select:none; }
.upgrade-summary::-webkit-details-marker { display:none; }
.upgrade-summary:hover { color:var(--ink); }
.upgrade-summary-right { display:inline-flex; align-items:center; gap:5px; color:var(--muted); }
.upgrade-summary-hint { font-size:11.5px; font-weight:500; }
.upgrade-chevron { width:14px; height:14px; transition:transform .18s ease; }
.upgrade-disclosure[open] .upgrade-chevron { transform:rotate(180deg); }
.upgrade-body { display:grid; gap:9px; padding-top:10px; }
.deployment-tabs { display:inline-flex; flex-wrap:wrap; align-items:center; gap:3px; padding:3px; border-radius:9px; background:var(--panel); border:1px solid var(--line); width:fit-content; max-width:100%; }
.deployment-tab { height:26px; padding:0 10px; border:0; border-radius:6px; background:transparent; color:var(--muted); font-size:12px; font-weight:550; cursor:pointer; white-space:nowrap; transition:background .15s, color .15s, box-shadow .15s; }
.deployment-tab:hover:not(.active) { color:var(--ink); }
.deployment-tab.active { background:var(--card); color:var(--ink); box-shadow:0 1px 2px rgba(0, 0, 0, .06); }
.cmd-box { display:flex; align-items:center; gap:8px; padding:5px 6px 5px 11px; border:1px solid var(--line); border-radius:10px; background:var(--panel); min-width:0; }
.cmd-scroll { flex:1 1 auto; min-width:0; display:flex; align-items:center; gap:7px; overflow-x:auto; scrollbar-width:none; padding:4px 0; }
.cmd-scroll::-webkit-scrollbar { display:none; }
.cmd-prompt { font-family:ui-monospace,monospace; font-size:12px; font-weight:600; color:var(--muted); user-select:none; flex-shrink:0; }
.cmd-scroll code { font-family:ui-monospace,monospace; font-size:12px; line-height:1.4; color:var(--ink); white-space:nowrap; user-select:all; }
.cmd-copy-btn { flex-shrink:0; display:inline-flex; align-items:center; gap:5px; height:28px; padding:0 10px; border:1px solid var(--line); border-radius:7px; background:var(--card); color:var(--ink); font-size:12px; font-weight:550; cursor:pointer; white-space:nowrap; transition:background .15s, border-color .15s, color .15s; }
.cmd-copy-btn svg { width:13px; height:13px; }
.cmd-copy-btn:hover:not(.copied) { border-color:var(--accent); color:var(--accent); }
.cmd-copy-btn.copied { border-color:transparent; background:var(--ok-soft); color:var(--ok); }
.desktop-upgrade-row { display:flex; align-items:center; justify-content:space-between; gap:12px; flex-wrap:wrap; }
.upgrade-hint { font-size:11.5px; line-height:1.5; color:var(--muted); margin:0; }
.version-history { border-top:1px solid var(--line); padding-top:12px; display:grid; gap:8px; }
h3 { font-size:13.5px; font-weight:600; margin:0; }
.history-heading { display:flex; align-items:center; justify-content:space-between; }
.version-link { color:var(--accent); font-size:12px; }
.release-item summary { display:flex; flex-wrap:wrap; align-items:center; gap:8px; cursor:pointer; padding:5px 0; }
.release-item pre { margin:4px 0 6px; padding:10px; background:var(--panel); border-radius:8px; white-space:pre-wrap; overflow-wrap:anywhere; font-size:11.5px; line-height:1.6; }
time { margin-left:auto; color:var(--muted); font-size:11px; }
@media(max-width:480px) { .version-actions > span { flex-basis:100%; } .deployment-tabs { width:100%; } .deployment-tab { flex:1 1 auto; text-align:center; padding:0 6px; } }
</style>
