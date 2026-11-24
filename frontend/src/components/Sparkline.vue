<script setup lang="ts">
// A compact trend line: no axes, one series in the accent, scaled to its own range.
import { computed } from 'vue'
import type { PowerSample } from '../types'

const props = withDefaults(defineProps<{ samples: PowerSample[]; width?: number; height?: number }>(), {
  width: 64,
  height: 20,
})

const path = computed(() => {
  const s = props.samples
  if (s.length < 2) return ''
  const t0 = s[0][0]
  const t1 = s[s.length - 1][0]
  let lo = Infinity
  let hi = -Infinity
  for (const [, w] of s) {
    lo = Math.min(lo, w)
    hi = Math.max(hi, w)
  }
  // Keep flat-ish lines flat: a few watts of standby wobble shouldn't fill the sparkline
  const span = Math.max(hi - lo, hi * 0.15, 20)
  const mid = (hi + lo) / 2
  const pad = 2
  const x = (t: number) => pad + ((t - t0) / Math.max(t1 - t0, 1)) * (props.width - pad * 2)
  const y = (w: number) => props.height / 2 - ((w - mid) / span) * (props.height - pad * 2)
  return s.map(([t, w], i) => `${i ? 'L' : 'M'}${x(t).toFixed(1)},${y(w).toFixed(1)}`).join(' ')
})
</script>

<template>
  <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" class="spark" aria-hidden="true">
    <path v-if="path" :d="path" />
  </svg>
</template>

<style scoped>
.spark path {
  fill: none;
  stroke: var(--accent);
  stroke-width: 1.5;
  stroke-linecap: round;
  stroke-linejoin: round;
}
</style>
