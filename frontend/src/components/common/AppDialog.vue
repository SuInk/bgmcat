<script lang="ts">
// 叠着开的弹窗（比如「添加链接」上面再开「下载选项」）按一次 Esc 只关最上面那个
const openStack: symbol[] = [];
</script>

<script setup lang="ts">
import { onMounted, onUnmounted } from "vue";

import AppNavIcon from "../layout/AppNavIcon.vue";

/**
 * 全站统一的居中弹窗：点遮罩空白处或按 Esc 关闭，标题 + 说明 + 正文 + 底部操作。
 * 各页面只写内容；宽度、内边距、按钮间距都在这里定一次，别再各自手搓 fixed inset-0。
 * dismissible=false 用在提交中：不让用户在请求半路把弹窗关掉。
 */
const props = withDefaults(
  defineProps<{
    title: string;
    description?: string;
    size?: "sm" | "md" | "lg";
    dismissible?: boolean;
  }>(),
  { size: "md", dismissible: true }
);
const emit = defineEmits<{ close: [] }>();

const id = Symbol("dialog");

function close(): void {
  if (props.dismissible) {
    emit("close");
  }
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape" && openStack[openStack.length - 1] === id) {
    close();
  }
}

onMounted(() => {
  openStack.push(id);
  window.addEventListener("keydown", onKeydown);
});

onUnmounted(() => {
  openStack.splice(openStack.indexOf(id), 1);
  window.removeEventListener("keydown", onKeydown);
});
</script>

<template>
  <Teleport to="body">
    <div class="dialog-backdrop" @click.self="close">
      <section class="dialog-panel" :class="`dialog-${size}`" role="dialog" aria-modal="true" :aria-label="title">
        <header class="dialog-head">
          <div class="grid min-w-0 gap-1">
            <h3 class="dialog-title"><slot name="title">{{ title }}</slot></h3>
            <p v-if="description || $slots.description" class="dialog-desc">
              <slot name="description">{{ description }}</slot>
            </p>
          </div>
          <button type="button" class="icon-btn shrink-0" aria-label="关闭" :disabled="!dismissible" @click="close">
            <AppNavIcon name="close" aria-hidden="true" />
          </button>
        </header>
        <div v-if="$slots.default" class="dialog-body"><slot /></div>
        <footer v-if="$slots.footer" class="dialog-foot"><slot name="footer" /></footer>
      </section>
    </div>
  </Teleport>
</template>
