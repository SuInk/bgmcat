<script setup lang="ts">
/**
 * 全站图标统一走圆润风格（对齐 Diana 控制台）。
 * 对 Lucide 原生棱角较硬的几个高频框形图标（grid / home / panel-left / play）
 * 专门使用大圆角曲率（rx=2.6~4.5）重绘，消除方框感。
 */
import { computed } from "vue";
import type { Component } from "vue";
import {
  ArrowDownToLine,
  BookOpen,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Copy,
  CornerLeftUp,
  Cloud,
  CloudUpload,
  Database,
  Eye,
  EyeOff,
  ExternalLink,
  File,
  FileArchive,
  FileAudio,
  FileClock,
  FileImage,
  FileText,
  FileVideo,
  Folder,
  FolderClosed,
  FolderPlus,
  Globe,
  HardDrive,
  HardDriveDownload,
  History,
  Inbox,
  Info,
  KeyRound,
  LayoutGrid,
  Link2,
  List,
  Lock,
  LogOut,
  Menu,
  Moon,
  Pause,
  Plus,
  PlugZap,
  RefreshCw,
  RotateCcw,
  Rss,
  Search,
  Share2,
  Sparkles,
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
  manga: BookOpen,
  favorites: Star,
  history: History,
  subscriptions: Rss,
  downloads: HardDriveDownload,
  files: FolderClosed,
  logs: FileClock,
  integrations: PlugZap,
  settings: Wrench,
  // 文件浏览
  folder: Folder,
  "folder-plus": FolderPlus,
  "file-video": FileVideo,
  "file-audio": FileAudio,
  "file-image": FileImage,
  "file-text": FileText,
  "file-archive": FileArchive,
  file: File,
  "hard-drive": HardDrive,
  "corner-left-up": CornerLeftUp,
  // 设置分类
  database: Database,
  info: Info,
  globe: Globe,
  book: BookOpen,
  cloud: Cloud,
  "cloud-upload": CloudUpload,
  key: KeyRound,
  // 外壳
  menu: Menu,
  theme: SunMoon,
  sun: Sun,
  moon: Moon,
  tag: Tag,
  logout: LogOut,
  // 通用操作
  close: X,
  pause: Pause,
  trash: Trash2,
  eye: Eye,
  "eye-off": EyeOff,
  restore: RotateCcw,
  "chevron-down": ChevronDown,
  "chevron-left": ChevronLeft,
  "chevron-right": ChevronRight,
  plus: Plus,
  download: ArrowDownToLine,
  check: Check,
  search: Search,
  sparkles: Sparkles,
  refresh: RefreshCw,
  external: ExternalLink,
  copy: Copy,
  share: Share2,
  link: Link2,
  lock: Lock,
  inbox: Inbox,
  list: List
};

const props = defineProps<{
  name: string;
}>();

const icon = computed(() => ICONS[props.name] ?? LayoutGrid);
</script>

<template>
  <!-- 圆润四宫格（首页新番 & 网格视图切换）：用 rx=2.6 替代原本 rx=1 的尖角方格 -->
  <svg
    v-if="name === 'home' || name === 'grid'"
    class="size-5"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.85"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <rect x="3" y="3" width="7.5" height="7.5" rx="2.6" />
    <rect x="13.5" y="3" width="7.5" height="7.5" rx="2.6" />
    <rect x="13.5" y="13.5" width="7.5" height="7.5" rx="2.6" />
    <rect x="3" y="13.5" width="7.5" height="7.5" rx="2.6" />
  </svg>

  <!-- 圆润侧栏收起/展开图标：用 rx=5 的大圆角外框替代原本 rx=2 的方框 -->
  <svg
    v-else-if="name === 'panel-left'"
    class="size-5"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.85"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <rect x="3" y="3" width="18" height="18" rx="5" />
    <path d="M9 3v18" />
    <path d="m15.5 14.5-2.5-2.5 2.5-2.5" />
  </svg>

  <svg
    v-else-if="name === 'panel-left-open'"
    class="size-5"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.85"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <rect x="3" y="3" width="18" height="18" rx="5" />
    <path d="M9 3v18" />
    <path d="m13.5 9.5 2.5 2.5-2.5 2.5" />
  </svg>

  <!-- 圆角播放三角 -->
  <svg
    v-else-if="name === 'play'"
    class="size-5"
    viewBox="0 0 24 24"
    fill="currentColor"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M7.5 5.65C7.5 4.48 8.78 3.76 9.78 4.38L18.65 9.85C19.6 10.44 19.6 11.82 18.65 12.41L9.78 17.88C8.78 18.5 7.5 17.78 7.5 16.61V5.65Z" />
  </svg>

  <component
    v-else
    :is="icon"
    class="size-5"
    :stroke-width="1.85"
    aria-hidden="true"
  />
</template>
