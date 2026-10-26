<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import { getCloudStatus, connectCloud, listCloudFolders, createCloudFolder, checkCloudFolder } from "../../api";
import type { CloudFolder } from "../../api";
import { settingsForm, saveSettingsFields, settingsSaveError } from "../../stores/session";
import { openExternalLink, copyText } from "../../lib/actions";

const emit = defineEmits<{ connected: [] }>();
const opened = ref(localStorage.getItem("bgmcat.storage.setup.dismissed") !== "1");
const step = ref(0);
const connected = ref(false);
const busy = ref(false);
const error = ref("");
const note = ref("");
const clientId = ref("");
const secret = ref("");
const callback = ref("");
const suggestedCallback = `${location.origin}/api/cloud/callback`;
const folders = ref<CloudFolder[]>([]);
const trail = ref<CloudFolder[]>([{ id: "root", name: "我的云盘" }]);
const current = computed(() => trail.value[trail.value.length - 1]!);
const pageToken = ref("");
const selected = ref<CloudFolder>({ id: "root", name: "我的云盘" });
const verified = ref(false);
const removeLocal = ref(false);
let timer: ReturnType<typeof setInterval> | undefined;
let expires = 0;
async function run(fn: () => Promise<void>) {
 if (busy.value) return;
 busy.value = true; error.value = ""; note.value = "";
 try { await fn(); } catch (e) { error.value = e instanceof Error ? e.message : String(e); }
 finally { busy.value = false; }
}
async function save(keys: Parameters<typeof saveSettingsFields>[0]) {
 await saveSettingsFields(keys);
 if (settingsSaveError.value) throw new Error(settingsSaveError.value);
}
function dismiss() { opened.value = false; localStorage.setItem("bgmcat.storage.setup.dismissed", "1"); }
function start() {
 opened.value = true; step.value = connected.value ? 2 : 1;
 clientId.value = settingsForm.googleClientId || "";
 secret.value = settingsForm.googleClientSecret || "";
 callback.value = settingsForm.googleRedirectUri || suggestedCallback;
 if (connected.value) void run(loadFolders);
}
defineExpose({ start });
async function refresh() {
 const status = await getCloudStatus();
 const justConnected = status.connected && !connected.value;
 connected.value = status.connected;
 if (justConnected) {
  emit("connected");
  if (timer) clearInterval(timer);
  timer = undefined;
  if (opened.value && step.value === 1) { step.value = 2; if (busy.value) await loadFolders(); else await run(loadFolders); }
 }
}
async function importJSON(event: Event) {
 const input = event.target as HTMLInputElement;
 const file = input.files?.[0]; if (!file) return;
 await run(async () => {
  if (file.size > 65536) throw new Error("凭据文件过大，请选择 Google 下载的 OAuth JSON。");
  let parsed;
  try { parsed = JSON.parse(await file.text()); } catch { throw new Error("不是有效的 JSON 文件。"); }
  const web = parsed?.web;
  if (!web || typeof web.client_id !== "string" || typeof web.client_secret !== "string" || !web.client_id.endsWith(".apps.googleusercontent.com") || !web.client_secret) throw new Error("请选择 Web application 类型的 OAuth JSON；不支持服务账号或 Desktop 类型凭据。");
  clientId.value = web.client_id; secret.value = web.client_secret;
  callback.value = suggestedCallback;
  note.value = Array.isArray(web.redirect_uris) && web.redirect_uris.includes(suggestedCallback)
   ? "凭据已导入，回调地址匹配。点击连接完成授权。"
   : "凭据已导入。请先将下方回调地址添加到 Google 客户端的授权重定向 URI。";
 }); input.value = "";
}
async function authorize() {
 await run(async () => {
  if (!clientId.value.trim() || !secret.value.trim()) throw new Error("请先导入凭据 JSON，或手动填写客户端 ID 和密钥。");
  settingsForm.googleClientId = clientId.value.trim(); settingsForm.googleClientSecret = secret.value.trim(); settingsForm.googleRedirectUri = callback.value.trim();
  await save(["googleClientId", "googleClientSecret", "googleRedirectUri"]);
  const result = await connectCloud(); await openExternalLink(result.url);
  note.value = "请在浏览器完成授权。完成后这里会自动进入下一步；也可点击检查授权状态。";
  expires = Date.now() + 10 * 60 * 1000;
  if (timer) clearInterval(timer);
  timer = setInterval(() => { if (Date.now() > expires) { clearInterval(timer); timer = undefined; } else void refresh().catch(() => {}); }, 3000);
 });
}
async function loadFolders(more = false) {
 const result = await listCloudFolders(current.value.id, more ? pageToken.value : "");
 folders.value = more ? [...folders.value, ...result.files] : result.files;
 pageToken.value = result.nextPageToken || "";
}
async function enter(folder: CloudFolder) {
 await run(async () => { const old = [...trail.value]; trail.value.push(folder); try { await loadFolders(); } catch (e) { trail.value = old; throw e; } });
}
async function back() {
 await run(async () => { const old = [...trail.value]; trail.value.pop(); try { await loadFolders(); } catch(e) { trail.value = old; throw e; } });
}
async function createFolder() {
 await run(async () => { const folder = await createCloudFolder(current.value.id); selected.value = folder; step.value = 3; });
}
function selectCurrent() { selected.value = { ...current.value }; step.value = 3; }
watch(selected, () => { verified.value = false; });
async function check() { await run(async () => { verified.value = false; const result = await checkCloudFolder(selected.value.id); verified.value = result.verified; }); }
async function finish() {
 if (!verified.value) return;
 await run(async () => {
  settingsForm.cloudDriveFolderId = selected.value.id;
  await save(["cloudDriveFolderId"]);
  settingsForm.cloudDeleteLocal = removeLocal.value; settingsForm.cloudArchiveEnabled = true;
  await save(["cloudDeleteLocal", "cloudArchiveEnabled"]);
  step.value = 4; emit("connected");
 });
}
const focusRefresh = () => { void refresh().catch(() => {}); };
onMounted(() => { focusRefresh(); window.addEventListener("focus", focusRefresh); });
onUnmounted(() => { if (timer) clearInterval(timer); window.removeEventListener("focus", focusRefresh); });
</script>

<template>
 <section class="cloud-setup" aria-label="存储新手引导">
  <div class="setup-heading"><div><h3>让番剧有个家</h3><p>本地保存，或连接 Google 云盘。</p></div><button type="button" v-if="!opened" class="settings-secondary-button" @click="start">配置向导</button></div>
  <template v-if="opened">
   <p class="setup-progress">{{ ['选择存储方式', '1 / 3 · 连接 Google', '2 / 3 · 选择目录', '3 / 3 · 检测并完成', '配置完成'][step] }}</p>
   <template v-if="step === 0">
    <p>云盘归档会先下载到本地，再上传并校验。你可以选择校验成功后清理本地文件。</p>
    <div class="setup-actions"><button type="button" class="settings-secondary-button" @click="start">连接 Google 云盘</button><button type="button" class="settings-secondary-button" @click="dismiss">暂时使用本地存储</button></div>
   </template>
   <template v-if="step === 1">
    <details><summary>第一次使用？展开 Google 控制台配置步骤</summary>
     <ol><li><button type="button" class="setup-link" @click="openExternalLink('https://console.cloud.google.com/')">打开 Google Cloud 控制台</button>，新建项目并启用 Google Drive API。</li><li>配置 OAuth 同意屏幕；External 测试应用需将自己的邮箱加入测试用户。</li><li>创建 Web application 类型 OAuth 客户端，将下面的回调地址添加到授权重定向 URI，然后下载 JSON。</li></ol>
     <p>当前需要你自己的 OAuth 客户端。External 测试模式的云盘授权通常需在 7 天后重新连接。</p>
    </details>
    <label class="setup-field">导入 OAuth 凭据 JSON<input type="file" accept=".json,application/json" :disabled="busy" @change="importJSON" /></label>
    <label class="setup-field">回调地址<input v-model="callback" class="settings-input settings-input-wide" :disabled="busy" /></label>
    <div class="setup-actions"><button type="button" class="settings-secondary-button" @click="run(async () => { await copyText(callback); note = '已复制回调地址'; })">复制回调地址</button><button type="button" class="settings-secondary-button" @click="callback = suggestedCallback">使用当前访问地址</button></div>
    <details><summary>手动填写客户端凭据</summary><label class="setup-field">Client ID<input v-model="clientId" class="settings-input settings-input-wide" :disabled="busy" /></label><label class="setup-field">Client Secret<input v-model="secret" type="password" autocomplete="off" class="settings-input settings-input-wide" :disabled="busy" /></label></details>
    <div class="setup-actions"><button type="button" class="settings-secondary-button" :disabled="busy" @click="authorize">连接 Google</button><button type="button" class="settings-secondary-button" :disabled="busy" @click="run(async () => { await refresh(); if (!connected) note = '尚未完成授权，请在浏览器中登录。'; })">检查授权状态</button><button type="button" class="settings-secondary-button" :disabled="busy" @click="dismiss">稍后配置</button></div>
   </template>
   <template v-if="step === 2">
    <p>当前目录：{{ trail.map(f => f.name).join(' / ') }}</p>
    <div class="setup-actions"><button type="button" v-if="trail.length > 1" class="settings-secondary-button" :disabled="busy" @click="back">上一级</button><button type="button" class="settings-secondary-button" :disabled="busy" @click="run(loadFolders)">刷新目录</button></div>
    <ul class="setup-folders"><li v-for="folder in folders" :key="folder.id"><button type="button" :disabled="busy" @click="enter(folder)">📁 {{ folder.name }} →</button></li></ul>
    <p v-if="!folders.length && !busy">没有子文件夹，可使用当前目录或新建。</p>
    <button type="button" v-if="pageToken" class="settings-secondary-button" :disabled="busy" @click="run(() => loadFolders(true))">加载更多文件夹</button>
    <div class="setup-actions"><button type="button" class="settings-secondary-button" :disabled="busy" @click="selectCurrent">使用当前目录</button><button type="button" class="settings-secondary-button" :disabled="busy" @click="createFolder">新建 Bangumi Cat 并使用</button></div>
   </template>
   <template v-if="step === 3">
    <p>保存到：{{ selected.name }}</p><p>检测会上传并读回一个临时小文件，然后删除它。</p>
    <div class="setup-actions"><button type="button" class="settings-secondary-button" :disabled="busy" @click="check">{{ busy ? '正在处理…' : verified ? '重新检测' : '检测上传与读取' }}</button><button type="button" class="settings-secondary-button" :disabled="busy" @click="step = 2">更换目录</button></div>
    <p v-if="verified" role="status">✓ 上传、读取校验和测试文件清理均已通过。</p>
    <label class="setup-choice"><input v-model="removeLocal" type="checkbox" :disabled="busy" />归档校验成功后清理本地文件</label><p>默认保留本地副本。开启后，仅在云端校验通过、可读取且未播放时清理。</p>
    <button type="button" class="settings-secondary-button" :disabled="busy || !verified" @click="finish">完成并启用自动归档</button>
   </template>
   <template v-if="step === 4"><p>已开启自动归档，保存到 {{ selected.name }}。{{ removeLocal ? '归档校验成功后清理本地文件。' : '保留本地副本。' }}</p><button type="button" class="settings-secondary-button" @click="dismiss">完成</button></template>
   <p v-if="busy && step !== 3" role="status">正在处理…</p><p v-if="note" role="status">{{ note }}</p><p v-if="error" role="alert" class="setup-error">{{ error }}</p>
  </template>
 </section>
</template>

<style scoped>
/* 放在设置卡片里当一行用：分隔线由 .settings-group 画，这里不再自带边框和外边距 */
.cloud-setup { padding: 0.75rem 0.875rem; overflow-wrap: anywhere; }
.setup-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; }
h3 { color: var(--ink); font-size: 0.875rem; font-weight: 600; }
p { margin: 6px 0; color: var(--muted); font-size: 0.75rem; line-height: 1.7; }
.setup-heading p { margin: 2px 0 0; }
.setup-progress { color: var(--ink); font-weight: 600; }
.setup-actions { display: flex; flex-wrap: wrap; gap: 8px; margin: 12px 0; }
.setup-field { display: grid; gap: 6px; margin: 12px 0; font-size: 0.8125rem; }
input { max-width: 100%; min-width: 0; }
details { margin: 12px 0; font-size: 0.8125rem; }
summary { cursor: pointer; }
ol { margin: 10px 0; padding-left: 22px; color: var(--muted); font-size: 0.8125rem; line-height: 1.9; list-style: decimal; }
.setup-link { color: var(--accent-vivid); text-decoration: underline; }
.setup-folders { max-height: 220px; margin: 12px 0; overflow: auto; border-radius: 0.5rem; border: 1px solid var(--line); }
.setup-folders button { width: 100%; padding: 8px 10px; text-align: left; font-size: 0.8125rem; }
.setup-folders li + li { border-top: 1px solid var(--line); }
.setup-folders button:hover { background: var(--tint-2); }
.setup-choice { display: flex; align-items: center; gap: 10px; font-size: 0.8125rem; }
.setup-error { color: var(--err); }
button:disabled { cursor: wait; opacity: 0.5; }
@media (max-width: 480px) { .setup-actions > button { flex: 1 1 140px; } }
</style>
