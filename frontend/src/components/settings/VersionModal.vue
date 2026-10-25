<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import VersionPanel from "./VersionPanel.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement | null>(null);
onMounted(() => dialog.value?.showModal());
onUnmounted(() => dialog.value?.close());
</script>
<template>
 <Teleport to="body"><dialog ref="dialog" class="version-modal" aria-labelledby="version-dialog-title" @cancel.prevent="emit('close')" @click="event => { if (event.target === dialog) emit('close'); }">
  <div class="modal-surface"><header><h2 id="version-dialog-title">版本与更新</h2><button type="button" aria-label="关闭版本与更新" autofocus @click="emit('close')"><AppNavIcon name="close" /></button></header><div class="modal-body"><VersionPanel /></div></div>
 </dialog></Teleport>
</template>
<style scoped>
.version-modal { border:1px solid var(--line); border-radius:18px; padding:0; margin:auto; width:min(720px,calc(100vw - 32px)); max-height:calc(100dvh - 48px); background:var(--app-bg); color:var(--ink); box-shadow:var(--strong-shadow); overflow:hidden; }.version-modal::backdrop { background:rgb(0 0 0 / .45); backdrop-filter:blur(3px); }.modal-surface { display:flex; flex-direction:column; max-height:calc(100dvh - 50px); }.modal-surface > header { display:flex; align-items:center; justify-content:space-between; padding:16px 20px; border-bottom:1px solid var(--line); flex-shrink:0; }h2 { font-size:16px; font-weight:650; }header button { display:grid; place-items:center; width:32px; height:32px; border-radius:8px; color:var(--muted); }header button:hover { background:var(--panel); }.modal-body { padding:20px; overflow:auto; min-height:0; }@media(max-width:480px) { .version-modal { width:calc(100vw - 16px); max-height:calc(100dvh - 24px); }.modal-surface { max-height:calc(100dvh - 26px); }.modal-body { padding:14px; } }
</style>
