<script setup lang="ts">
import { computed } from 'vue'
import ConfirmHost from './components/ConfirmHost.vue'
import ReinstallDialog from './components/ReinstallDialog.vue'
import ScheduleDialog from './components/ScheduleDialog.vue'
import ServerPanel from './components/ServerPanel.vue'
import Toasts from './components/Toasts.vue'
import { activeOperations, serverById, store } from './store'
import { showView, ui } from './ui'
import ActivityView from './views/ActivityView.vue'
import BootImagesView from './views/BootImagesView.vue'
import ServersView from './views/ServersView.vue'

const runningCount = computed(() => activeOperations.value.filter((op) => op.status !== 'scheduled').length)
const dialogServer = computed(() => (ui.dialog ? serverById(ui.dialog.serverId) : undefined))
const connectionLabel = computed(
  () => ({ live: 'Live', connecting: 'Connecting', reconnecting: 'Reconnecting' })[store.connection],
)
</script>

<template>
  <div class="app" :class="{ 'panel-open': !!ui.serverId }">
    <header class="topbar">
      <div class="brand">
        <svg width="20" height="20" viewBox="0 0 24 24" aria-hidden="true" class="mark">
          <rect x="3" y="4" width="18" height="6" rx="2" />
          <rect x="3" y="14" width="18" height="6" rx="2" />
          <circle cx="7" cy="7" r="1.2" class="led" />
          <circle cx="7" cy="17" r="1.2" class="led" />
        </svg>
        <span>Lab Manager</span>
      </div>
      <nav class="nav" aria-label="Sections">
        <button class="tab" :aria-current="ui.view === 'servers' ? 'page' : undefined" @click="showView('servers')">Servers</button>
        <button class="tab" :aria-current="ui.view === 'boot' ? 'page' : undefined" @click="showView('boot')">Boot images</button>
        <button class="tab" :aria-current="ui.view === 'activity' ? 'page' : undefined" @click="showView('activity')">
          Activity
          <span v-if="runningCount" class="count num" :aria-label="`${runningCount} running`">{{ runningCount }}</span>
        </button>
      </nav>
      <div class="conn" :class="`conn-${store.connection}`" role="status" :title="store.connection === 'live' ? 'Receiving live updates' : 'Lost contact with Lab Manager; showing the last known state'">
        <span class="conn-dot" aria-hidden="true" />
        {{ connectionLabel }}
      </div>
    </header>

    <div v-if="store.connection === 'reconnecting'" class="banner" role="alert">
      Lost contact with Lab Manager. Showing the last known state while it reconnects.
    </div>

    <main class="main">
      <ServersView v-if="ui.view === 'servers'" />
      <BootImagesView v-else-if="ui.view === 'boot'" />
      <ActivityView v-else />
    </main>

    <ServerPanel />
    <ScheduleDialog v-if="ui.dialog?.type === 'schedule' && dialogServer" :server="dialogServer" @close="ui.dialog = null" />
    <ReinstallDialog v-if="ui.dialog?.type === 'reinstall' && dialogServer" :server="dialogServer" @close="ui.dialog = null" />
    <ConfirmHost />
    <Toasts />
  </div>
</template>

<style scoped>
.app {
  min-height: 100vh;
}
.topbar {
  position: sticky;
  top: 0;
  z-index: 30;
  display: flex;
  align-items: center;
  gap: 24px;
  height: 56px;
  padding: 0 24px;
  background: color-mix(in srgb, var(--bg) 88%, transparent);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--border);
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  font-weight: 600;
  letter-spacing: -0.01em;
  white-space: nowrap;
}
.mark rect {
  fill: none;
  stroke: currentColor;
  stroke-width: 1.8;
}
.mark .led {
  fill: var(--success);
}
.nav {
  display: flex;
  gap: 2px;
  flex: 1;
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 12px;
  border: 0;
  border-radius: 8px;
  background: none;
  color: var(--text-2);
  font: inherit;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 150ms var(--ease), color 150ms var(--ease);
}
.tab:hover {
  color: var(--text);
  background: var(--surface-2);
}
.tab[aria-current='page'] {
  color: var(--text);
  background: var(--surface);
  box-shadow: var(--shadow-sm), inset 0 0 0 1px var(--border);
}
.count {
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--accent);
  color: #fff;
  font-size: 11px;
  line-height: 18px;
  text-align: center;
}
.conn {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  color: var(--text-2);
  white-space: nowrap;
}
.conn-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--success);
}
.conn-connecting .conn-dot {
  background: var(--text-3);
}
.conn-reconnecting .conn-dot {
  background: var(--warning);
  animation: blink 1.2s ease-in-out infinite;
}
@keyframes blink {
  50% {
    opacity: 0.3;
  }
}
.banner {
  padding: 10px 24px;
  background: var(--warning-soft);
  color: var(--warning-text);
  font-size: 13.5px;
}
.main {
  max-width: 1320px;
  margin: 0 auto;
  padding: 28px 24px 64px;
  transition: padding-right 200ms var(--ease);
}
/* Keep the table visible beside the panel on wide screens */
@media (min-width: 1400px) {
  .panel-open .main {
    margin-right: 560px;
    margin-left: 0;
  }
}
@media (max-width: 720px) {
  .topbar {
    gap: 12px;
    padding: 0 16px;
  }
  .brand span {
    display: none;
  }
  .nav {
    overflow-x: auto;
  }
  .tab {
    padding: 0 10px;
  }
  .conn {
    font-size: 0;
    gap: 0;
  }
  .main {
    padding: 20px 16px 48px;
  }
}
</style>
