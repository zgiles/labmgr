<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref } from 'vue'
import type { ActionGroup, ActionItem } from '../actions'
import Icon from './Icon.vue'

defineProps<{ groups: ActionGroup[]; label: string }>()

const open = ref(false)
const trigger = ref<HTMLButtonElement>()
const menu = ref<HTMLDivElement>()
const pos = ref({ top: 0, left: 0, up: false })

function items(): HTMLButtonElement[] {
  return Array.from(menu.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])
}

async function toggle() {
  if (open.value) return close()
  const rect = trigger.value!.getBoundingClientRect()
  const up = rect.bottom + 360 > window.innerHeight && rect.top > 360
  pos.value = {
    top: up ? rect.top - 6 : rect.bottom + 6,
    left: Math.max(8, Math.min(rect.right - 248, window.innerWidth - 256)),
    up,
  }
  open.value = true
  await nextTick()
  items()[0]?.focus()
  document.addEventListener('pointerdown', onOutside, true)
  window.addEventListener('scroll', close, true)
  window.addEventListener('resize', close)
}

function close() {
  open.value = false
  document.removeEventListener('pointerdown', onOutside, true)
  window.removeEventListener('scroll', close, true)
  window.removeEventListener('resize', close)
}

function onOutside(e: Event) {
  if (!menu.value?.contains(e.target as Node) && !trigger.value?.contains(e.target as Node)) close()
}

function choose(item: ActionItem) {
  close()
  trigger.value?.focus()
  item.run()
}

function onKey(e: KeyboardEvent) {
  const list = items()
  const i = list.indexOf(document.activeElement as HTMLButtonElement)
  if (e.key === 'Escape') {
    e.preventDefault()
    close()
    trigger.value?.focus()
  } else if (e.key === 'ArrowDown') {
    e.preventDefault()
    list[(i + 1) % list.length]?.focus()
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    list[(i - 1 + list.length) % list.length]?.focus()
  } else if (e.key === 'Tab') {
    close()
  }
}

onBeforeUnmount(close)
defineExpose({ toggle })
</script>

<template>
  <button ref="trigger" class="icon-btn" :aria-label="label" aria-haspopup="menu" :aria-expanded="open" @click.stop="toggle">
    <Icon name="more" :size="18" />
  </button>
  <Teleport to="body">
    <div
      v-if="open"
      ref="menu"
      class="menu"
      role="menu"
      :style="{ top: `${pos.top}px`, left: `${pos.left}px`, transform: pos.up ? 'translateY(-100%)' : undefined }"
      @keydown="onKey"
      @click.stop
    >
      <template v-for="(group, g) in groups" :key="g">
        <div v-if="g > 0" class="sep" role="separator" />
        <div v-if="group.label" class="group-label">{{ group.label }}</div>
        <button
          v-for="item in group.items"
          :key="item.label"
          class="item"
          :class="{ danger: item.danger }"
          role="menuitem"
          :disabled="item.disabled"
          @click="choose(item)"
        >
          <span>{{ item.label }}</span>
          <span v-if="item.hint" class="hint">{{ item.hint }}</span>
        </button>
      </template>
    </div>
  </Teleport>
</template>

<style scoped>
.menu {
  position: fixed;
  z-index: 50;
  width: 248px;
  max-height: min(70vh, 520px);
  overflow-y: auto;
  padding: 6px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-lg);
}
.group-label {
  padding: 6px 10px 4px;
  font-size: 12px;
  color: var(--text-3);
}
.sep {
  height: 1px;
  margin: 6px 4px;
  background: var(--border);
}
.item {
  display: grid;
  width: 100%;
  padding: 7px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: background-color 120ms var(--ease);
}
.item:hover:not(:disabled),
.item:focus-visible {
  background: var(--surface-2);
  outline: none;
}
.item.danger:not(:disabled) {
  color: var(--danger-text);
}
.item:disabled {
  color: var(--text-3);
  cursor: default;
}
.hint {
  font-size: 12px;
  color: var(--text-2);
}
.item:disabled .hint {
  color: var(--text-3);
}
</style>
