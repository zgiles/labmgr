<script setup lang="ts">
import { computed } from 'vue'
import { stateInfo } from '../format'
import type { DisplayState } from '../types'

const props = defineProps<{ state: DisplayState; title?: string }>()
const info = computed(() => stateInfo[props.state] ?? stateInfo.unknown)
</script>

<template>
  <span class="state" :class="`tone-${info.tone}`" :title="title">
    <span class="dot" :class="{ live: info.live }" aria-hidden="true" />
    {{ info.label }}
  </span>
</template>

<style scoped>
.state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
  white-space: nowrap;
}
.dot {
  position: relative;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--neutral);
  flex: none;
}
.tone-success .dot {
  background: var(--success);
}
.tone-warning .dot {
  background: var(--warning);
}
.tone-danger .dot {
  background: var(--danger);
}
.tone-accent .dot {
  background: var(--accent);
}
.tone-muted .dot {
  background: var(--text-3);
}
.tone-muted {
  color: var(--text-2);
}
.dot.live::after {
  content: '';
  position: absolute;
  inset: -3px;
  border-radius: 50%;
  background: inherit;
  filter: blur(3px);
  animation: pulse 2s var(--ease) infinite;
}
@keyframes pulse {
  0%,
  100% {
    opacity: 0.15;
    transform: scale(0.8);
  }
  50% {
    opacity: 0.7;
    transform: scale(1.25);
  }
}
</style>
