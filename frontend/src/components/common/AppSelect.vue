<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";

import AppNavIcon from "../layout/AppNavIcon.vue";

export interface AppSelectOption {
  value: string | number;
  label: string;
  disabled?: boolean;
}

const props = withDefaults(
  defineProps<{
    modelValue: string | number | undefined;
    options: AppSelectOption[];
    ariaLabel?: string;
    prefix?: string;
    disabled?: boolean;
    align?: "left" | "right";
    direction?: "down" | "up";
    size?: "sm" | "md";
  }>(),
  {
    ariaLabel: undefined,
    prefix: undefined,
    disabled: false,
    align: "left",
    direction: "down",
    size: "md"
  }
);

const emit = defineEmits<{
  (event: "update:modelValue", value: string | number): void;
  (event: "change", value: string | number): void;
}>();

const rootRef = ref<HTMLElement | null>(null);
const triggerRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLElement | null>(null);
const open = ref(false);
const menuStyle = ref<Record<string, string>>({});
// 打开的原生 <dialog> 在浏览器顶层，挂到 body 上的菜单会被它压住；在对话框里就挂进对话框。
const teleportTarget = ref<string | HTMLElement>("body");

const selectedOption = computed(
  () => props.options.find((opt) => opt.value === props.modelValue) ?? props.options[0] ?? null
);

function updateMenuPosition(): void {
  const trigger = triggerRef.value ?? rootRef.value;
  if (!trigger) return;
  const rect = trigger.getBoundingClientRect();
  const minW = Math.max(Math.round(rect.width), 152);
  const estimatedH = Math.min(props.options.length * 38 + 10, 256);
  const spaceBelow = window.innerHeight - rect.bottom - 10;
  const spaceAbove = rect.top - 10;
  const openUp = props.direction === "up" ? spaceAbove >= Math.min(estimatedH, 120) || spaceAbove > spaceBelow : spaceBelow < estimatedH && spaceAbove > spaceBelow;

  const style: Record<string, string> = {
    minWidth: `${minW}px`,
    maxWidth: `min(22rem, calc(100vw - 16px))`
  };
  if (props.align === "right") {
    style.right = `${Math.max(8, Math.round(window.innerWidth - rect.right))}px`;
  } else {
    const left = Math.min(rect.left, window.innerWidth - minW - 8);
    style.left = `${Math.max(8, Math.round(left))}px`;
  }
  if (openUp) {
    style.bottom = `${Math.max(8, Math.round(window.innerHeight - rect.top + 6))}px`;
  } else {
    style.top = `${Math.max(8, Math.round(rect.bottom + 6))}px`;
  }
  menuStyle.value = style;
}

function bindViewportListeners(): void {
  window.addEventListener("resize", updateMenuPosition);
  window.addEventListener("scroll", handleViewportScroll, true);
}

function unbindViewportListeners(): void {
  window.removeEventListener("resize", updateMenuPosition);
  window.removeEventListener("scroll", handleViewportScroll, true);
}

function handleViewportScroll(event: Event): void {
  if (!open.value) return;
  const target = event.target as Node | null;
  if (target && menuRef.value?.contains(target)) {
    return;
  }
  updateMenuPosition();
}

function toggle(): void {
  if (props.disabled) {
    return;
  }
  if (open.value) {
    close();
    return;
  }
  teleportTarget.value = (rootRef.value?.closest("dialog[open]") as HTMLElement | null) ?? "body";
  updateMenuPosition();
  open.value = true;
  bindViewportListeners();
  void nextTick(updateMenuPosition);
}

function close(): void {
  if (!open.value) return;
  open.value = false;
  unbindViewportListeners();
}

function selectOption(option: AppSelectOption): void {
  if (option.disabled) {
    return;
  }
  close();
  if (option.value !== props.modelValue) {
    emit("update:modelValue", option.value);
  }
  emit("change", option.value);
}

function handleDocumentPointerDown(event: PointerEvent): void {
  if (!open.value) {
    return;
  }
  const target = event.target as Node | null;
  if (target && (rootRef.value?.contains(target) || menuRef.value?.contains(target))) {
    return;
  }
  close();
}

function handleKeydown(event: KeyboardEvent): void {
  if (!open.value) {
    return;
  }
  if (event.key === "Escape") {
    event.stopPropagation();
    close();
    return;
  }
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    const enabled = props.options.filter((opt) => !opt.disabled);
    if (!enabled.length) {
      return;
    }
    const idx = enabled.findIndex((opt) => opt.value === props.modelValue);
    const delta = event.key === "ArrowDown" ? 1 : -1;
    const next = enabled[(idx + delta + enabled.length) % enabled.length];
    if (next) {
      emit("update:modelValue", next.value);
      emit("change", next.value);
    }
  }
}

onMounted(() => {
  document.addEventListener("pointerdown", handleDocumentPointerDown);
});

onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", handleDocumentPointerDown);
  unbindViewportListeners();
});
</script>

<template>
  <div ref="rootRef" class="app-select" :class="[`is-${size}`, { 'is-open': open, 'is-disabled': disabled }]" @keydown="handleKeydown">
    <button
      ref="triggerRef"
      type="button"
      class="app-select-trigger"
      :disabled="disabled"
      :aria-label="ariaLabel"
      aria-haspopup="listbox"
      :aria-expanded="open"
      @click="toggle"
    >
      <span v-if="prefix" class="app-select-prefix">{{ prefix }}</span>
      <span class="app-select-value">{{ selectedOption?.label ?? "" }}</span>
      <svg class="app-select-caret" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
        <path d="M3 4.5L6 7.5L9 4.5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>

    <Teleport :to="teleportTarget">
      <div
        v-if="open"
        ref="menuRef"
        class="app-select-menu"
        :style="menuStyle"
        role="listbox"
        :aria-label="ariaLabel"
        @keydown="handleKeydown"
      >
        <button
          v-for="opt in options"
          :key="String(opt.value)"
          type="button"
          role="option"
          class="app-select-option"
          :class="{ 'is-active': opt.value === modelValue }"
          :aria-selected="opt.value === modelValue"
          :disabled="opt.disabled"
          @click="selectOption(opt)"
        >
          <span class="app-select-option-check" aria-hidden="true">
            <AppNavIcon v-if="opt.value === modelValue" name="check" class="size-3.5" />
          </span>
          <span class="min-w-0 flex-1 truncate text-left">{{ opt.label }}</span>
        </button>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.app-select {
  position: relative;
  display: inline-flex;
  flex-shrink: 0;
}

.app-select-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 2.25rem;
  padding: 0 11px;
  border-radius: var(--radius-md);
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink);
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
  transition: border-color 150ms ease, background 150ms ease, box-shadow 150ms ease;
}

.app-select.is-sm .app-select-trigger {
  min-height: 2.125rem;
  padding: 0 10px;
  border-radius: 8px;
  font-size: 12px;
}

.app-select-trigger:hover:not(:disabled) {
  border-color: var(--accent-line);
  background: var(--tint-1);
}

.app-select.is-open .app-select-trigger,
.app-select-trigger:focus-visible {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
  outline: none;
}

.app-select-trigger:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.app-select-prefix {
  color: var(--muted);
  font-weight: 500;
}

.app-select-value {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.app-select-caret {
  width: 12px;
  height: 12px;
  flex-shrink: 0;
  color: var(--muted);
  transition: transform 150ms ease;
}

.app-select.is-open .app-select-caret {
  transform: rotate(180deg);
}

@media (pointer: coarse) {
  .app-select-trigger {
    min-height: 2.5rem;
  }
}
</style>

<style>
.app-select-menu {
  position: fixed;
  z-index: 200;
  display: grid;
  gap: 2px;
  max-height: 16rem;
  overflow-y: auto;
  padding: 4px;
  border-radius: 12px;
  border: 1px solid var(--line);
  background: var(--card);
  box-shadow: var(--strong-shadow);
}

.app-select-option {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  min-height: 2.25rem;
  padding: 6px 10px 6px 8px;
  border-radius: 8px;
  color: var(--ink);
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
  transition: background 120ms ease, color 120ms ease;
}

.app-select-option:hover:not(:disabled) {
  background: var(--panel);
}

.app-select-option.is-active {
  background: var(--accent-soft);
  color: var(--accent-vivid);
  font-weight: 600;
}

.app-select-option:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}

.app-select-option-check {
  display: inline-grid;
  place-items: center;
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}

@media (pointer: coarse) {
  .app-select-option {
    min-height: 2.5rem;
  }
}
</style>
