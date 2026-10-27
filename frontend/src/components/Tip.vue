<script setup lang="ts">
// Hover/focus tooltip for a cell's explanation. The trigger stays focusable for keyboards.
import { nextTick, ref } from 'vue'

defineProps<{ text?: string }>()

const open = ref(false)
const trigger = ref<HTMLElement>()
const bubble = ref<HTMLElement>()
const pos = ref({ top: 0, left: 0 })

async function show() {
  open.value = true
  await nextTick()
  const r = trigger.value!.getBoundingClientRect()
  const w = bubble.value?.offsetWidth ?? 240
  pos.value = {
    top: r.bottom + 6,
    left: Math.max(8, Math.min(r.left, window.innerWidth - w - 8)),
  }
}
function hide() {
  open.value = false
}
</script>

<template>
  <span ref="trigger" class="tip-trigger" :tabindex="text ? 0 : undefined" @mouseenter="text && show()" @mouseleave="hide" @focus="text && show()" @blur="hide">
    <slot />
  </span>
  <Teleport to="body">
    <div v-if="open && text" ref="bubble" class="tip" role="tooltip" :style="{ top: `${pos.top}px`, left: `${pos.left}px` }">{{ text }}</div>
  </Teleport>
</template>

<style scoped>
.tip-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border-radius: 4px;
}
.tip-trigger:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.tip {
  position: fixed;
  z-index: 80;
  max-width: 300px;
  padding: 7px 10px;
  border-radius: 8px;
  background: var(--text);
  color: var(--bg);
  font-size: 12.5px;
  line-height: 1.45;
  box-shadow: var(--shadow-md);
  pointer-events: none;
  white-space: pre-line;
  animation: tip-in 120ms var(--ease);
}
@keyframes tip-in {
  from {
    opacity: 0;
    transform: translateY(-2px);
  }
}
</style>
