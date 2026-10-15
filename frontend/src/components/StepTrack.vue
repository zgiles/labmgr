<script setup lang="ts">
// The operation's steps as a segmented track: done, current, still to come.
// Compact for table rows; `detailed` lists every step with timing and errors.
import { computed } from 'vue'
import { duration } from '../format'
import type { Operation } from '../types'

const props = defineProps<{ op: Operation; detailed?: boolean }>()

const current = computed(() => props.op.steps[props.op.current])
const caption = computed(() => {
  const op = props.op
  if (op.status === 'failed') return op.error ?? 'Failed'
  if (op.status === 'succeeded') return op.note || 'Done'
  if (op.status === 'cancelled') return 'Cancelled'
  if (op.status === 'interrupted') return op.error ?? 'Interrupted'
  const step = current.value
  if (!step) return 'Starting'
  let text = step.label
  if (step.attempts > 1) text += ` (attempt ${step.attempts})`
  return text
})
</script>

<template>
  <div class="track" :class="[`op-${op.status}`, { detailed }]">
    <div class="segments" role="progressbar" :aria-valuemin="0" :aria-valuemax="op.steps.length" :aria-valuenow="op.current" :aria-label="op.label">
      <span v-for="(step, i) in op.steps" :key="i" class="seg" :class="`seg-${step.status}`" :title="step.label" />
    </div>
    <div v-if="!detailed" class="caption" :title="caption">
      <span class="num faint">{{ Math.min(op.current + 1, op.steps.length) }}/{{ op.steps.length }}</span>
      {{ caption }}
    </div>
    <ol v-else class="steps">
      <li v-for="(step, i) in op.steps" :key="i" :class="`step-${step.status}`">
        <span class="marker" aria-hidden="true" />
        <div class="step-body">
          <div class="step-line">
            <span>{{ step.label }}</span>
            <span v-if="step.started_at" class="faint num">{{ duration(step.started_at, step.finished_at) }}</span>
          </div>
          <div v-if="step.attempts > 1 && step.status !== 'failed'" class="faint small">Attempt {{ step.attempts }}</div>
          <div v-if="step.error && (step.status === 'failed' || step.status === 'running')" class="step-error">{{ step.error }}</div>
        </div>
      </li>
    </ol>
  </div>
</template>

<style scoped>
.track {
  display: grid;
  gap: 6px;
  min-width: 0;
}
.segments {
  display: flex;
  gap: 3px;
  height: 4px;
}
.seg {
  flex: 1;
  border-radius: 2px;
  background: var(--surface-3);
  transition: background-color 200ms var(--ease);
}
.seg-succeeded {
  background: var(--accent);
}
.seg-running {
  background: linear-gradient(90deg, var(--accent) 0%, var(--accent-soft) 50%, var(--accent) 100%);
  background-size: 200% 100%;
  animation: flow 1.6s linear infinite;
}
.seg-failed {
  background: var(--danger);
}
.op-succeeded .seg-succeeded {
  background: var(--success);
}
.seg-skipped {
  background: var(--surface-2);
}
@keyframes flow {
  from {
    background-position: 200% 0;
  }
  to {
    background-position: 0 0;
  }
}
.caption {
  font-size: 12.5px;
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.op-failed .caption {
  color: var(--danger-text);
}
.steps {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  display: grid;
  gap: 2px;
}
.steps li {
  display: grid;
  grid-template-columns: 16px 1fr;
  gap: 10px;
  padding: 5px 0;
  font-size: 13px;
}
.marker {
  width: 10px;
  height: 10px;
  margin-top: 5px;
  border-radius: 50%;
  border: 2px solid var(--surface-3);
  background: var(--surface);
}
.step-succeeded .marker {
  border-color: var(--accent);
  background: var(--accent);
}
.op-succeeded .step-succeeded .marker {
  border-color: var(--success);
  background: var(--success);
}
.step-running .marker {
  border-color: var(--accent);
}
.step-failed .marker {
  border-color: var(--danger);
  background: var(--danger);
}
.step-skipped,
.step-pending {
  color: var(--text-2);
}
.step-line {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}
.small {
  font-size: 12px;
}
.step-error {
  font-size: 12px;
  color: var(--danger-text);
  overflow-wrap: anywhere;
}
.step-running .step-error {
  color: var(--warning-text);
}
</style>
