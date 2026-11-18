<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { isDesktopShell, status } from "../../stores/session";
import { updateVersion, updateStatus, updateStatusLabel, updateStatusTone, updateReleases, updateBusy, refreshUpdateOverview, runUpdateCheck } from "../../stores/update";
import { copyText, openExternalLink } from "../../lib/actions";
import AppSelect from "../common/AppSelect.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";

const loading = ref(true);
const deployment = ref(isDesktopShell ? "desktop" : "server");
const copied = ref(false);
const copyError = ref("");
const versionLabel = computed(() => `v${(updateVersion.value?.version || status.value?.appVersion || "未知").replace(/^v/, "")}`);
const phaseLabel = computed(() => updateBusy.value ? "检查中…" : updateStatus.value?.kind === "error" ? "检查失败" : updateStatusLabel.value);
const windows = computed(() => updateVersion.value?.platform === "windows");
const installCommand = computed(() => deployment.value === "source" ? "git pull --ff-only\nsh scripts/build.sh" : deployment.value === "docker" ? "docker compose pull\ndocker compose up -d" : windows.value
 ? '$installer = gh api repos/SuInk/bgmcat/contents/scripts/install.ps1 -H \'Accept: application/vnd.github.raw+json\'\nif ($LASTEXITCODE -ne 0) { throw \'下载安装脚本失败\' }\n& ([scriptblock]::Create(($installer -join "`n"))) -Private -Start'
 : "gh api repos/SuInk/bgmcat/contents/scripts/install.sh -H 'Accept: application/vnd.github.raw+json' | sh -s -- --private --start");
function date(value: string | null | undefined) { return value ? new Date(value).toLocaleString() : ""; }
async function copyCommand() { copyError.value = ""; try { await copyText(installCommand.value); copied.value = true; } catch { copyError.value = "复制失败，请手动选择命令复制。"; } }
onMounted(async () => { try { await refreshUpdateOverview(); } finally { loading.value = false; } });
</script>

<template>
 <div class="version-panel" :aria-busy="loading || updateBusy">
  <header class="version-hero">
   <div class="version-heading"><strong class="version-current">{{ versionLabel }}</strong><template v-if="updateStatus?.updateAvailable && updateStatus.latestVersion"><span aria-hidden="true">→</span><strong class="version-latest">{{ updateStatus.latestVersion }}</strong></template><span class="version-badge" :class="`tone-${updateStatusTone}`" role="status">{{ loading ? '读取中…' : phaseLabel }}</span></div>
   <div class="version-meta"><span>{{ updateVersion ? `${updateVersion.platform} / ${updateVersion.arch}` : '正在读取运行平台' }}</span><span v-if="updateStatus?.checkedAt">{{ date(updateStatus.checkedAt) }} 检查过</span></div>
   <p v-if="updateStatus?.latestPublishedAt" class="version-note">最新版本发布于 {{ date(updateStatus.latestPublishedAt) }}</p>
   <p v-if="updateStatus?.lastError" class="version-error" role="alert">{{ updateStatus.lastError }}</p>
   <div v-if="loading || updateBusy" class="scope-progress" role="progressbar" aria-label="正在检查版本"><span /></div>
  </header>
  <div class="version-options">
   <label>更新通道<AppSelect size="sm" model-value="stable" disabled aria-label="更新通道" :options="[{ value: 'stable', label: '稳定版' }]" /></label>
   <label>下载来源<AppSelect size="sm" model-value="github" disabled aria-label="下载来源" :options="[{ value: 'github', label: 'GitHub 官方仓库' }]" /></label>
  </div>
  <div class="version-actions"><span class="version-note">{{ updateStatus?.supported ? '' : '自动安装暂不可用' }}</span><button class="version-button" :disabled="loading || updateBusy" @click="runUpdateCheck"><AppNavIcon name="refresh" :class="{ 'animate-spin': updateBusy }" />{{ updateBusy ? '检查中…' : '检查更新' }}</button><button class="version-button" @click="openExternalLink('https://github.com/SuInk/bgmcat/releases')"><AppNavIcon name="external" />查看发布版本</button></div>
  <p class="version-note">{{ updateStatus?.unsupportedReason || '当前版本尚不支持应用内自动安装。请选择下面与你安装方式一致的升级步骤。' }}</p>
  <section class="upgrade-section">
   <h3>升级方式</h3>
   <label class="deployment-label">当前安装方式<AppSelect size="sm" :model-value="deployment" aria-label="当前安装方式" :options="[{ value: 'desktop', label: '桌面安装包' }, { value: 'server', label: '服务端安装包' }, { value: 'source', label: '源码运行' }, { value: 'docker', label: 'Docker' }]" @update:model-value="deployment = String($event)" @change="copied = false" /></label>
   <template v-if="deployment === 'desktop'"><p>退出 Bangumi Cat，下载与你系统和架构匹配的桌面安装包，校验 SHA-256 后覆盖安装。保留应用数据目录，重新打开即可继续使用。</p><button class="version-button primary" @click="openExternalLink('https://github.com/SuInk/bgmcat/releases')">下载桌面安装包</button></template>
   <template v-else>
    <p v-if="deployment === 'server'">先停止原服务，再用安装脚本升级；保持原来的安装目录和数据目录。私有仓库需先执行 gh auth login，并拥有仓库读取权限。需要仓库已有正式 Release。</p>
    <p v-else-if="deployment === 'source'">在项目目录中操作，先处理未提交改动并停止服务。拉取代码、重新构建后，用原数据目录和启动参数启动新程序。</p>
    <p v-else>在 Compose 文件所在目录操作，保留数据卷。以下命令适用于已配置发布镜像的部署；如果使用本地源码构建，请拉取代码后执行 docker compose up -d --build。</p>
    <pre><code>{{ installCommand }}</code></pre><button class="version-button" @click="copyCommand">{{ copied ? '已复制' : '复制升级命令' }}</button><p v-if="copyError" class="version-error">{{ copyError }}</p>
   </template>
  </section>
  <section class="version-history"><div class="history-heading"><h3>版本历史</h3><button class="version-link" @click="openExternalLink('https://github.com/SuInk/bgmcat/releases')">查看全部 ↗</button></div>
   <p v-if="!updateReleases.length" class="version-note">{{ updateStatus?.kind === 'no_release' ? '当前没有可用的稳定发布版本。' : '暂无版本记录，点击检查更新获取。' }}</p>
   <details v-for="release in updateReleases" :key="release.tag"><summary><code>{{ release.tag }}</code><span v-if="release.prerelease" class="version-badge">预发布</span><time>{{ date(release.publishedAt) }}</time></summary><pre>{{ release.notes || '此版本未提供更新说明。' }}</pre></details>
  </section>
 </div>
</template>

<style scoped>
.version-panel { display:grid; gap:14px; color:var(--ink); font-size:13px; min-width:0; }.version-hero { position:relative; overflow:hidden; padding:18px; border:1px solid var(--line); border-radius:16px; background:var(--card); }.version-heading { display:flex; align-items:center; gap:10px; flex-wrap:wrap; }.version-current,.version-latest { font-family:ui-monospace,monospace; font-size:22px; font-weight:650; }.version-latest { color:var(--accent); }.version-badge { padding:3px 10px; border-radius:999px; font-size:11px; font-weight:600; background:var(--panel); color:var(--muted); }.tone-ok { color:var(--ok); background:var(--ok-soft); }.tone-err { color:var(--err); background:var(--err-soft); }.tone-accent { color:var(--accent); background:var(--accent-soft); }.tone-warn { color:var(--warn); background:var(--warn-soft); }.version-meta { display:flex; flex-wrap:wrap; gap:8px 16px; color:var(--muted); font-size:11.5px; margin-top:10px; }.version-note { color:var(--muted); font-size:12px; line-height:1.7; }.version-error { color:var(--err); line-height:1.7; margin-top:8px; overflow-wrap:anywhere; }.version-options { display:grid; grid-template-columns:1fr 1fr; gap:14px; }.version-options label { display:grid; gap:7px; }select { border:1px solid var(--line); border-radius:11px; padding:8px 11px; background:var(--input-bg); color:var(--ink); min-width:0; }select:disabled { opacity:.65; }.version-actions { display:flex; flex-wrap:wrap; gap:8px; align-items:center; }.version-actions > span { margin-right:auto; }.version-button { display:inline-flex; justify-content:center; align-items:center; gap:7px; min-height:36px; border:1px solid var(--line); border-radius:11px; padding:8px 14px; background:var(--card); font-size:12px; font-weight:550; transition:background .15s, border-color .15s; }.version-button svg { width:14px; height:14px; }.version-button:disabled { opacity:.5; cursor:wait; }.version-button:hover:not(:disabled) { border-color:var(--accent); background:var(--panel); }.version-button.primary { border-color:var(--accent); color:var(--accent-contrast); background:var(--accent); }.upgrade-section,.version-history { border-top:1px solid var(--line); padding-top:14px; display:grid; gap:10px; }h3 { font-size:14px; font-weight:600; }.deployment-label { display:flex; align-items:center; justify-content:space-between; gap:12px; }.upgrade-section p { font-size:12px; line-height:1.8; color:var(--muted); }pre { padding:12px; background:var(--panel); border-radius:12px; white-space:pre-wrap; overflow-wrap:anywhere; font-size:11.5px; line-height:1.7; }.history-heading { display:flex; align-items:center; justify-content:space-between; }.version-link { color:var(--accent); font-size:12px; }summary { display:flex; flex-wrap:wrap; gap:10px; cursor:pointer; padding:8px 0; }time { margin-left:auto; color:var(--muted); font-size:11px; }
@media(max-width:480px) { .version-options { gap:8px; }.version-actions > span { flex-basis:100%; }.version-button { min-height:42px; } }
</style>
