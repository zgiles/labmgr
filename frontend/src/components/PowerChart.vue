<script setup lang="ts">
// Power draw over time for one server: a single series, so the title names it and
// there's no legend. Crosshair snaps to the nearest reading.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { errorText } from '../store'
import type { PowerSample, Server } from '../types'

const props = defineProps<{ server: Server }>()

const ranges = [
  { hours: 1, label: '1h' },
  { hours: 6, label: '6h' },
  { hours: 24, label: '24h' },
  { hours: 168, label: '7d' },
]
const hours = ref(1)
const samples = ref<PowerSample[]>([])
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    samples.value = await api.power(props.server.id, hours.value)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}
watch([() => props.server.id, hours], load, { immediate: true })

// Append live readings while the short range is showing
watch(
  () => props.server.state.watts_at,
  (at) => {
    if (!at || props.server.state.watts == null || hours.value > 6) return
    const t = new Date(at).getTime()
    const last = samples.value[samples.value.length - 1]
    if (last && last[0] >= t) return
    // Slide the window: drop readings that have aged out of the range
    const cutoff = Date.now() - hours.value * 3600_000
    samples.value = [...samples.value.filter(([ts]) => ts >= cutoff), [t, props.server.state.watts]]
  },
)

const stats = computed(() => {
  const s = samples.value
  if (!s.length) return null
  let sum = 0
  let peak = -Infinity
  for (const [, w] of s) {
    sum += w
    peak = Math.max(peak, w)
  }
  return { avg: Math.round(sum / s.length), peak: Math.round(peak) }
})

// Geometry
const box = ref<HTMLDivElement>()
const width = ref(480)
const height = 104
const pad = { top: 6, right: 12, bottom: 20, left: 44 }
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(([entry]) => (width.value = Math.max(240, entry.contentRect.width)))
  observer.observe(box.value!)
})
onBeforeUnmount(() => observer?.disconnect())

const scale = computed(() => {
  const s = samples.value
  const now = Date.now()
  const t0 = now - hours.value * 3600_000
  let hi = 0
  for (const [, w] of s) hi = Math.max(hi, w)
  // Round the top up to a clean step so gridlines land on readable numbers
  const step = hi <= 50 ? 10 : hi <= 200 ? 50 : hi <= 1000 ? 100 : 500
  const top = Math.max(step, Math.ceil((hi * 1.1) / step) * step)
  const plotW = width.value - pad.left - pad.right
  const plotH = height - pad.top - pad.bottom
  return {
    t0,
    t1: now,
    top,
    x: (t: number) => pad.left + ((t - t0) / (now - t0)) * plotW,
    y: (w: number) => pad.top + plotH - (w / top) * plotH,
  }
})

const linePath = computed(() => {
  const { x, y } = scale.value
  const s = samples.value
  if (s.length < 2) return ''
  // Break the line across gaps (BMC unreachable, labmgr stopped) instead of bridging them
  const gap = Math.max(5 * 60_000, (hours.value * 3600_000) / 100)
  return s
    .map(([t, w], i) => `${i === 0 || t - s[i - 1][0] > gap ? 'M' : 'L'}${x(t).toFixed(1)},${y(w).toFixed(1)}`)
    .join(' ')
})

const gridlines = computed(() => {
  const { top, y } = scale.value
  return [0, top / 2, top].map((w) => ({ w, y: y(w) }))
})

const timeTicks = computed(() => {
  const { t0, t1, x } = scale.value
  const fmt: Intl.DateTimeFormatOptions =
    hours.value > 24 ? { weekday: 'short' } : { hour: 'numeric', minute: hours.value <= 6 ? '2-digit' : undefined }
  return [0, 1 / 3, 2 / 3, 1].map((f) => {
    const t = t0 + (t1 - t0) * f
    return { x: x(t), label: f === 1 ? 'now' : new Date(t).toLocaleString(undefined, fmt), anchor: f === 0 ? 'start' : f === 1 ? 'end' : 'middle' }
  })
})

// Crosshair
const hover = ref<{ x: number; y: number; t: number; w: number } | null>(null)
function onMove(e: PointerEvent) {
  const s = samples.value
  if (!s.length) return
  const rect = (e.currentTarget as SVGElement).getBoundingClientRect()
  const px = e.clientX - rect.left
  const { x, y } = scale.value
  let best = s[0]
  for (const p of s) if (Math.abs(x(p[0]) - px) < Math.abs(x(best[0]) - px)) best = p
  hover.value = { x: x(best[0]), y: y(best[1]), t: best[0], w: best[1] }
}
const hoverTime = computed(() =>
  hover.value
    ? new Date(hover.value.t).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
    : '',
)
</script>

<template>
  <section class="power">
    <div class="head">
      <h3 class="section-title">Power draw</h3>
      <div class="figures num">
        <span class="now">{{ server.state.watts != null ? `${Math.round(server.state.watts)} W` : '—' }}</span>
        <span v-if="stats" class="muted">avg {{ stats.avg }} · peak {{ stats.peak }}</span>
      </div>
      <div class="range" role="group" aria-label="Time range">
        <button v-for="r in ranges" :key="r.hours" class="seg" :aria-pressed="hours === r.hours" @click="hours = r.hours">{{ r.label }}</button>
      </div>
    </div>

    <div ref="box" class="plot">
      <div v-if="loading && !samples.length" class="skeleton" :style="{ height: `${height}px` }" />
      <p v-else-if="error" class="muted">{{ error }}</p>
      <p v-else-if="!samples.length" class="empty-plot muted small">
        No readings yet. Lab Manager records power draw once a minute while the BMC responds.
      </p>
      <svg
        v-else
        :width="width"
        :height="height"
        role="img"
        :aria-label="`Power draw over the last ${hours} hours; average ${stats?.avg} watts, peak ${stats?.peak} watts`"
        @pointermove="onMove"
        @pointerleave="hover = null"
      >
        <g class="grid">
          <g v-for="g in gridlines" :key="g.w">
            <line :x1="pad.left" :x2="width - pad.right" :y1="g.y" :y2="g.y" />
            <text :x="pad.left - 8" :y="g.y + 4" text-anchor="end">{{ g.w }} W</text>
          </g>
          <text v-for="tick in timeTicks" :key="tick.label" :x="tick.x" :y="height - 4" :text-anchor="tick.anchor">{{ tick.label }}</text>
        </g>
        <path class="line" :d="linePath" />
        <g v-if="hover">
          <line class="cross" :x1="hover.x" :x2="hover.x" :y1="pad.top" :y2="height - pad.bottom" />
          <circle class="dot" :cx="hover.x" :cy="hover.y" r="4" />
        </g>
      </svg>
      <div
        v-if="hover"
        class="readout"
        :style="{ left: `${Math.min(hover.x + 10, width - 120)}px`, top: `${Math.max(hover.y - 44, 0)}px` }"
      >
        <strong class="num">{{ Math.round(hover.w) }} W</strong>
        <span>{{ hoverTime }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 6px;
}
.head .section-title {
  margin: 0;
}
.range {
  display: inline-flex;
  padding: 2px;
  border-radius: 8px;
  background: var(--surface-2);
}
.seg {
  padding: 2px 9px;
  border: 0;
  border-radius: 6px;
  background: none;
  color: var(--text-2);
  font: inherit;
  font-size: 12.5px;
  cursor: pointer;
}
.seg[aria-pressed='true'] {
  background: var(--surface);
  color: var(--text);
  box-shadow: var(--shadow-sm);
}
.seg:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.figures {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex: 1;
  font-size: 12.5px;
}
.now {
  font-size: 15px;
  font-weight: 600;
}
.range {
  align-self: center;
}
.plot {
  position: relative;
}
svg {
  display: block;
  touch-action: none;
}
.grid line {
  stroke: var(--border);
  stroke-width: 1;
}
.grid text {
  fill: var(--text-3);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
.line {
  fill: none;
  stroke: var(--accent);
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}
.cross {
  stroke: var(--border-strong);
  stroke-width: 1;
}
.dot {
  fill: var(--accent);
  stroke: var(--surface);
  stroke-width: 2;
}
.readout {
  position: absolute;
  display: grid;
  padding: 6px 10px;
  border-radius: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  box-shadow: var(--shadow-md);
  font-size: 12px;
  color: var(--text-2);
  pointer-events: none;
  white-space: nowrap;
}
.readout strong {
  font-size: 14px;
  color: var(--text);
}
.empty-plot {
  padding: 8px 0;
}
.small {
  font-size: 13px;
}
</style>
