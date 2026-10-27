<script setup lang="ts">
/**
 * 全站图标统一走 Lucide（和 Diana 控制台同一套）。调用方只传语义名，
 * 换图标只改这张表；导航类的选择尽量和 Diana 对齐（日志 FileClock、
 * 插件/MCP PlugZap、设置 Wrench、主题 Sun/Moon/SunMoon、版本 Tag）。
 */
import { computed } from "vue";
import type { Component } from "vue";
import {
  BookOpen,
  ChevronLeft,
  ChevronRight,
  Copy,
  Cloud,
  Database,
  ExternalLink,
  FileClock,
  Globe,
  HardDriveDownload,
  Inbox,
  Info,
  KeyRound,
  LayoutGrid,
  List,
  LogOut,
  Menu,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Pause,
  Play,
  Plus,
  PlugZap,
  RefreshCw,
  Rss,
  Search,
  Star,
  Sun,
  SunMoon,
  Tag,
  Trash2,
  Wrench,
  X
} from "@lucide/vue";

const ICONS: Record<string, Component> = {
  // 主导航（键与 TabKey 一致）
  home: LayoutGrid,
  favorites: Star,
  subscriptions: Rss,
  downloads: HardDriveDownload,
  logs: FileClock,
  integrations: PlugZap,
  settings: Wrench,
  // 设置分类
  database: Database,
  info: Info,
  globe: Globe,
  book: BookOpen,
  cloud: Cloud,
  key: KeyRound,
  // 外壳
  "panel-left": PanelLeftClose,
  "panel-left-open": PanelLeftOpen,
  menu: Menu,
  theme: SunMoon,
  sun: Sun,
  moon: Moon,
  tag: Tag,
  logout: LogOut,
  // 通用操作
  close: X,
  play: Play,
  pause: Pause,
  trash: Trash2,
  "chevron-left": ChevronLeft,
  "chevron-right": ChevronRight,
  plus: Plus,
  search: Search,
  refresh: RefreshCw,
  external: ExternalLink,
  copy: Copy,
  inbox: Inbox,
  grid: LayoutGrid,
  list: List
};

const props = defineProps<{
  name: string;
}>();

const icon = computed(() => ICONS[props.name] ?? LayoutGrid);
</script>

<template>
  <component :is="icon" class="size-5" :stroke-width="2" aria-hidden="true" />
</template>
