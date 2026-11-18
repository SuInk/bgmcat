<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { LoaderCircle, PlugZap, RefreshCw } from "@lucide/vue";

import { isAndroidAppRuntime, openAndroidServerSwitcher, readAndroidServerLabel } from "../api";

const props = defineProps<{
  message?: string;
  retrying?: boolean;
  retryInSeconds: number;
  /** waiting 是宽限期：还在按秒重试，先别渲染成红色故障页 */
  waiting?: boolean;
  /** updating 表示这次断线是刚点过的升级造成的 */
  updating?: boolean;
  /** graceLeft 是宽限期剩余秒数，归零时整页重载一次 */
  graceLeft?: number;
}>();

const emit = defineEmits<{ retry: [] }>();

const isAndroidApp = isAndroidAppRuntime();
const androidServerLabel = computed(() => (isAndroidApp ? readAndroidServerLabel() : ""));

const countdown = ref(props.retryInSeconds);
let timer: ReturnType<typeof setInterval> | null = null;

function restart(): void {
  if (timer) clearInterval(timer);
  countdown.value = props.retryInSeconds;
  timer = setInterval(() => {
    if (countdown.value > 0) countdown.value -= 1;
  }, 1000);
}

watch(
  [() => props.retryInSeconds, () => props.retrying],
  ([, isRetrying]) => {
    if (!isRetrying) {
      restart();
    }
  },
  { immediate: true }
);

onBeforeUnmount(() => {
  if (timer) clearInterval(timer);
});
</script>

<template>
  <div class="backend-down" role="alert" aria-live="polite">
    <div class="backend-down-card">
      <span class="backend-down-mark" :class="{ waiting }">
        <LoaderCircle v-if="waiting" class="animate-spin" :size="22" aria-hidden="true" />
        <PlugZap v-else :size="22" aria-hidden="true" />
      </span>

      <template v-if="waiting">
        <h1>{{ updating ? "正在安装并重启" : "正在连接 Bangumi Cat" }}</h1>
        <p class="backend-down-lead">
          {{
            updating
              ? "新版本装好后旧进程会退出，由新进程接管，中间有十几秒没人应答。连上就会自动回到刚才的页面。"
              : "后端暂时没有应答，可能正在重启。稍等一下，连上会自动继续。"
          }}
        </p>
      </template>
      <template v-else>
        <h1>连不上 Bangumi Cat 后端</h1>
        <p class="backend-down-lead">页面本身是静态文件，浏览器能打开；但它背后的服务已经有一会儿没有应答了。</p>

        <p v-if="message" class="backend-down-detail">{{ message }}</p>

        <ul class="backend-down-hints">
          <li>确认 Bangumi Cat 进程还在运行，没有崩掉或正在重启。</li>
          <li>确认访问的地址和端口没写错；用了 Nginx 或 Cloudflare Tunnel 反代时，检查它能不能连上后端。</li>
          <li>刚触发过安装并重启的话，装完还要跑健康检查，可能要再多等一会儿。</li>
        </ul>
      </template>

      <div
        v-if="isAndroidApp && !waiting"
        class="flex items-center justify-between gap-2 rounded-lg border border-[var(--line)] bg-[var(--panel)] px-3 py-2 text-xs"
      >
        <span class="truncate text-[var(--muted)]">
          服务器：<strong class="font-semibold text-[var(--ink)]">{{ androidServerLabel }}</strong>
        </span>
        <button type="button" class="shrink-0 font-semibold text-[var(--accent)]" @click="openAndroidServerSwitcher()">
          切换地址
        </button>
      </div>

      <button
        class="backend-down-action"
        :class="waiting ? 'btn-ghost' : 'btn-primary'"
        type="button"
        :disabled="retrying"
        @click="emit('retry')"
      >
        <RefreshCw :size="15" :class="{ 'animate-spin': retrying }" aria-hidden="true" />
        <span>{{ retrying ? "正在重试…" : "立即重试" }}</span>
      </button>

      <p class="backend-down-countdown">
        {{ waiting ? `每秒自动重连中（${graceLeft}s）` : retrying ? "\u3000" : `${countdown} 秒后自动重试` }}
      </p>
    </div>
  </div>
</template>

<style scoped>
/* 1:1 对齐 Diana BackendDownView (.backend-down / .backend-down-card) */
.backend-down {
  min-height: 100dvh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--shell-bg);
  padding: 24px;
}

.backend-down-card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: min(460px, 100%);
  padding: 32px 28px;
  border-radius: var(--radius-lg, 16px);
  background: var(--card);
  border: 1px solid var(--line);
  box-shadow: var(--strong-shadow);
}

.backend-down-card h1 {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  color: var(--ink);
  text-align: center;
}

.backend-down-lead {
  margin: -6px 0 0;
  text-align: center;
  font-size: 13px;
  line-height: 1.6;
  color: var(--muted);
}

.backend-down-mark {
  align-self: center;
  width: 44px;
  height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 12px;
  background: color-mix(in srgb, var(--err, #e5484d) 14%, transparent);
  color: var(--err, #e5484d);
}

/* 宽限期内还没定性成故障，配色收敛，别一断线就满屏告警红 */
.backend-down-mark.waiting {
  background: color-mix(in srgb, var(--muted, #8b8b8b) 16%, transparent);
  color: var(--muted, #8b8b8b);
}

.backend-down-detail {
  margin: 0;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--shell-bg);
  border: 1px solid var(--line);
  color: var(--ink-secondary);
  font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12.5px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.backend-down-hints {
  margin: 0;
  padding-left: 18px;
  list-style: disc;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--muted);
}

.backend-down-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  height: 38px;
  width: 100%;
  border-radius: 10px;
  font-size: 13.5px;
  font-weight: 600;
}

.backend-down-countdown {
  margin: -6px 0 0;
  text-align: center;
  font-size: 12.5px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
</style>
