<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted } from 'vue'
import { serverActions } from '../actions'
import { stateExplanation } from '../format'
import { serverById } from '../store'
import { closeServer, setTab, ui, type PanelTab } from '../ui'
import ActionMenu from './ActionMenu.vue'
import Icon from './Icon.vue'
import PanelActivity from './PanelActivity.vue'
import PanelBoot from './PanelBoot.vue'
import PanelOverview from './PanelOverview.vue'
import StateBadge from './StateBadge.vue'

// xterm.js is large; load it only when someone opens a console
const PanelConsole = defineAsyncComponent(() => import('./PanelConsole.vue'))

const server = computed(() => serverById(ui.serverId ?? undefined))

const tabs = computed(() => {
  const list: { id: PanelTab; label: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'boot', label: 'Boot' },
    { id: 'activity', label: 'Activity' },
  ]
  if (server.value?.console.serial) list.push({ id: 'console', label: 'Console' })
  return list
})

function onKey(e: KeyboardEvent) {
  // Escape closes the panel unless a dialog or menu is handling it
  if (e.key === 'Escape' && !ui.dialog && !ui.confirm && !document.querySelector('.menu')) closeServer()
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <aside v-if="server" class="drawer" :class="{ wide: ui.tab === 'console' }" :aria-label="`${server.name} details`">
    <header class="head">
      <div class="title">
        <h2>{{ server.name }}</h2>
        <div class="line">
          <StateBadge :state="server.state.state" />
          <span class="muted">{{ stateExplanation(server) }}</span>
        </div>
      </div>
      <div class="head-actions">
        <ActionMenu :groups="serverActions(server)" :label="`Actions for ${server.name}`" />
        <button class="icon-btn" aria-label="Close panel" @click="closeServer"><Icon name="close" /></button>
      </div>
    </header>

    <nav class="tabs" role="tablist">
      <button
        v-for="t in tabs"
        :key="t.id"
        class="tab"
        role="tab"
        :aria-selected="ui.tab === t.id"
        @click="setTab(t.id)"
      >
        {{ t.label }}
      </button>
    </nav>

    <div class="content">
      <PanelOverview v-if="ui.tab === 'overview'" :server="server" />
      <PanelBoot v-else-if="ui.tab === 'boot'" :server="server" />
      <PanelActivity v-else-if="ui.tab === 'activity'" :server="server" />
      <PanelConsole v-else-if="ui.tab === 'console'" :key="server.id" :server="server" />
    </div>
  </aside>
  <aside v-else-if="ui.serverId" class="drawer">
    <div class="empty">
      There's no server called “{{ ui.serverId }}” any more.
      <div><button class="btn btn-sm" style="margin-top: 12px" @click="closeServer">Close</button></div>
    </div>
  </aside>
</template>

<style scoped>
.drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 40;
  width: min(560px, 100vw);
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border-left: 1px solid var(--border);
  box-shadow: var(--shadow-lg);
  animation: slide 200ms var(--ease);
  transition: width 200ms var(--ease);
}
.drawer.wide {
  width: min(960px, 100vw);
}
@keyframes slide {
  from {
    transform: translateX(24px);
    opacity: 0;
  }
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  padding: 20px 16px 12px 24px;
}
h2 {
  font-size: 22px;
  font-weight: 600;
  letter-spacing: -0.015em;
  line-height: 1.2;
}
.line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 12px;
  margin-top: 6px;
  font-size: 13px;
}
.head-actions {
  display: flex;
  gap: 2px;
}
.tabs {
  display: flex;
  gap: 4px;
  padding: 0 20px;
  border-bottom: 1px solid var(--border);
}
.tab {
  position: relative;
  padding: 10px 8px;
  border: 0;
  background: none;
  color: var(--text-2);
  font: inherit;
  font-weight: 500;
  cursor: pointer;
  border-radius: 6px 6px 0 0;
  transition: color 150ms var(--ease);
}
.tab:hover {
  color: var(--text);
}
.tab[aria-selected='true'] {
  color: var(--text);
}
.tab[aria-selected='true']::after {
  content: '';
  position: absolute;
  left: 8px;
  right: 8px;
  bottom: -1px;
  height: 2px;
  border-radius: 2px;
  background: var(--accent);
}
.content {
  flex: 1;
  overflow-y: auto;
  padding: 20px 24px 32px;
}
@media (max-width: 640px) {
  .head {
    padding: 16px 8px 10px 16px;
  }
  .tabs {
    padding: 0 8px;
  }
  .content {
    padding: 16px;
  }
}
</style>
