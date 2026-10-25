<script setup lang="ts">
defineProps<{
  name: string;
}>();
</script>

<template>
  <svg
    class="size-5"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <template v-if="name === 'tag'"><path d="M20 13 13 20a2 2 0 0 1-3 0l-8-8V3h9l9 7a2 2 0 0 1 0 3Z" /><circle cx="7" cy="7" r="1" /></template>
    <template v-else-if="name === 'panel-left'">
      <rect x="3" y="4" width="18" height="16" rx="2" /><path d="M9 4v16" />
    </template>
    <template v-else-if="name === 'home'">
      <path d="M4 12.5 12 5l8 7.5" />
      <path d="M6.5 11v8h11v-8" />
      <path d="M9.5 19v-5h5v5" />
    </template>
    <template v-else-if="name === 'favorites'">
      <path d="m12 3 2.5 5.2 5.7.8-4.1 4 1 5.7-5.1-2.7-5.1 2.7 1-5.7-4.1-4 5.7-.8L12 3Z" />
    </template>
    <template v-else-if="name === 'overview'">
      <path d="M4 18V8" />
      <path d="M9 18V5" />
      <path d="M14 18v-7" />
      <path d="M19 18V3" />
    </template>
    <template v-else-if="name === 'subscriptions'">
      <path d="M4 11a9 9 0 0 1 9 9" />
      <path d="M4 4a16 16 0 0 1 16 16" />
      <circle cx="5" cy="19" r="1.2" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="name === 'feed'">
      <path d="M4 5h10" />
      <path d="M4 12h16" />
      <path d="M4 19h12" />
      <path d="M18 5h2" />
    </template>
    <template v-else-if="name === 'downloads'">
      <path d="M12 4v10" />
      <path d="m8 10 4 4 4-4" />
      <path d="M5 19h14" />
    </template>
    <template v-else-if="name === 'logs'">
      <path d="M5 4h10l4 4v12H5z" />
      <path d="M15 4v5h5" />
      <path d="M8 13h8" />
      <path d="M8 17h5" />
    </template>
    <template v-else-if="name === 'integrations'">
      <path d="M8 7V4" />
      <path d="M16 7V4" />
      <path d="M7 7h10v5a5 5 0 0 1-10 0V7Z" />
      <path d="M12 17v3" />
    </template>
    <template v-else-if="name === 'settings'">
      <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2Z" />
      <circle cx="12" cy="12" r="3" />
    </template>
    <template v-else-if="name === 'close'">
      <path d="M6 6l12 12" />
      <path d="M18 6 6 18" />
    </template>
    <template v-else-if="name === 'play'">
      <path d="M8 5.5v13l10-6.5z" />
    </template>
    <template v-else-if="name === 'pause'">
      <path d="M9 5v14" />
      <path d="M15 5v14" />
    </template>
    <template v-else-if="name === 'trash'">
      <path d="M4 7h16" />
      <path d="M9 7V4h6v3" />
      <path d="M6.5 7 8 20h8l1.5-13" />
      <path d="M10 11v5" />
      <path d="M14 11v5" />
    </template>
    <template v-else-if="name === 'chevron-right'">
      <path d="m9 5 7 7-7 7" />
    </template>
    <template v-else-if="name === 'chevron-left'">
      <path d="m15 5-7 7 7 7" />
    </template>
    <template v-else-if="name === 'list'">
      <path d="M8 6h12" />
      <path d="M8 12h12" />
      <path d="M8 18h12" />
      <path d="M4 6h.01" />
      <path d="M4 12h.01" />
      <path d="M4 18h.01" />
    </template>
    <template v-else-if="name === 'plus'">
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </template>
    <template v-else-if="name === 'search'">
      <circle cx="11" cy="11" r="6.5" />
      <path d="m20 20-4.3-4.3" />
    </template>
    <template v-else-if="name === 'refresh'">
      <path d="M20 11a8 8 0 0 0-14.9-3" />
      <path d="M5 4v4h4" />
      <path d="M4 13a8 8 0 0 0 14.9 3" />
      <path d="M19 20v-4h-4" />
    </template>
    <template v-else-if="name === 'external'">
      <path d="M14 5h5v5" />
      <path d="M19 5 10.5 13.5" />
      <path d="M18 13.5V19H5V6h5.5" />
    </template>
    <template v-else-if="name === 'menu'">
      <path d="M4 7h16" />
      <path d="M4 12h16" />
      <path d="M4 17h16" />
    </template>
    <template v-else-if="name === 'inbox'">
      <path d="M4 5h16v14H4z" />
      <path d="M4 13h5a3 3 0 0 0 6 0h5" />
    </template>
    <template v-else-if="name === 'logout'">
      <path d="M13 4H5v16h8" />
      <path d="M10 12h10" />
      <path d="m16 8 4 4-4 4" />
    </template>
    <template v-else-if="name === 'theme'">
      <circle cx="12" cy="12" r="7.5" />
      <path d="M12 4.5v15A7.5 7.5 0 0 0 12 4.5Z" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="name === 'sun'">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 3v2" />
      <path d="M12 19v2" />
      <path d="M3 12h2" />
      <path d="M19 12h2" />
      <path d="m5.6 5.6 1.4 1.4" />
      <path d="m17 17 1.4 1.4" />
      <path d="m18.4 5.6-1.4 1.4" />
      <path d="m7 17-1.4 1.4" />
    </template>
    <template v-else-if="name === 'moon'">
      <path d="M20 13.5A8 8 0 0 1 10.5 4 8 8 0 1 0 20 13.5Z" />
    </template>
    <template v-else>
      <circle cx="12" cy="12" r="8" />
      <path d="M12 8v4" />
      <path d="M12 16h.01" />
    </template>
  </svg>
</template>
