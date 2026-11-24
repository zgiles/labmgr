<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import { actorName, dateTime, opStatusInfo, relativeTime } from '../format'
import { errorText, operationsFor, store, toast } from '../store'
import type { ActivityEvent, BMCLogEntry, Operation, Server } from '../types'
import StepTrack from './StepTrack.vue'

const props = defineProps<{ server: Server }>()

const ops = computed(() => operationsFor(props.server.id).slice(0, 30))
const expanded = ref<string | null>(null)

// History from the server, merged with anything that arrived live since
const history = ref<ActivityEvent[]>([])
const events = computed(() => {
  const seen = new Set<string>()
  return [...store.events.filter((e) => e.server_id === props.server.id), ...history.value]
    .filter((e) => (seen.has(e.id) ? false : (seen.add(e.id), true)))
    .sort((a, b) => b.at.localeCompare(a.at))
    .slice(0, 100)
})

const bmcLog = ref<BMCLogEntry[] | null>(null)
const bmcLogLoading = ref(false)
const bmcLogError = ref('')

watch(
  () => props.server.id,
  async (id) => {
    history.value = []
    bmcLog.value = null
    bmcLogError.value = ''
    try {
      history.value = await api.events(id, 100)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  },
  { immediate: true },
)

async function loadBMCLog() {
  bmcLogLoading.value = true
  bmcLogError.value = ''
  try {
    bmcLog.value = await api.bmcLogs(props.server.id, 50)
  } catch (e) {
    bmcLogError.value = errorText(e)
  } finally {
    bmcLogLoading.value = false
  }
}

async function retry(op: Operation) {
  try {
    const next = await api.retry(op.id)
    store.operations[next.id] = next
    expanded.value = next.id
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

const severityTone = (s: string) => (s === 'OK' ? '' : s === 'Warning' ? 'tone-warning' : 'tone-danger')
</script>

<template>
  <div class="stack">
    <section>
      <h3 class="section-title">Operations</h3>
      <p v-if="!ops.length" class="muted">Nothing has been done to {{ server.name }} yet.</p>
      <ul class="ops">
        <li v-for="op in ops" :key="op.id" :class="{ open: expanded === op.id }">
          <button class="op-row" :aria-expanded="expanded === op.id" @click="expanded = expanded === op.id ? null : op.id">
            <span class="op-label">{{ op.label.replace(` ${server.name}`, '') }}</span>
            <span class="badge" :class="`tone-${opStatusInfo[op.status].tone}`">{{ opStatusInfo[op.status].label }}</span>
            <span class="when num faint">{{ relativeTime(op.status === 'scheduled' ? op.scheduled_for : op.created_at) }}</span>
          </button>
          <div v-if="expanded === op.id" class="op-detail">
            <div class="meta faint">
              {{ dateTime(op.created_at) }}<template v-if="op.requested_by"> · {{ actorName(op.requested_by) }}</template>
              <template v-if="op.scheduled_for"> · scheduled for {{ dateTime(op.scheduled_for) }}</template>
            </div>
            <p v-if="op.error && !op.steps.some((st) => st.error === op.error)" class="err">{{ op.error }}</p>
            <p v-if="op.note" class="muted">{{ op.note }}</p>
            <StepTrack v-if="op.steps.length" :op="op" detailed />
            <a v-if="op.console_capture" class="capture-link" :href="api.captureUrl(server.id, op.console_capture)" target="_blank" rel="noopener">
              Console output from this {{ op.kind === 'reinstall' ? 'install' : 'boot' }}
            </a>
            <button v-if="['failed', 'cancelled', 'interrupted'].includes(op.status)" class="btn btn-sm" @click="retry(op)">
              Try again
            </button>
          </div>
        </li>
      </ul>
    </section>

    <section>
      <h3 class="section-title">History</h3>
      <p v-if="!events.length" class="muted">No activity recorded.</p>
      <ul class="events">
        <li v-for="e in events" :key="e.id" :class="`lvl-${e.level ?? 'info'}`">
          <span class="when num faint" :title="dateTime(e.at)">{{ relativeTime(e.at) }}</span>
          <span class="msg">{{ e.message }}<span v-if="e.actor" class="faint"> · {{ actorName(e.actor) }}</span></span>
        </li>
      </ul>
    </section>

    <section v-if="server.has_bmc">
      <h3 class="section-title">BMC event log</h3>
      <p v-if="!bmcLog && !bmcLogLoading" class="lede">
        Hardware events recorded by the BMC: power faults, sensor alerts, watchdog resets.
        <button class="btn btn-sm" :disabled="server.state.bmc !== 'ok'" @click="loadBMCLog">Load</button>
      </p>
      <div v-if="bmcLogLoading" class="skeleton" style="height: 80px" />
      <p v-if="bmcLogError" class="err">{{ bmcLogError }}</p>
      <p v-if="bmcLog && !bmcLog.length" class="muted">The BMC log has no events.</p>
      <ul v-if="bmcLog?.length" class="events">
        <li v-for="(e, i) in bmcLog" :key="i">
          <span class="when num faint">{{ dateTime(e.created) }}</span>
          <span class="msg">
            <span v-if="e.severity && e.severity !== 'OK'" class="badge" :class="severityTone(e.severity)">{{ e.severity }}</span>
            {{ e.message }}
          </span>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.stack {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 32px;
}
.ops,
.events {
  list-style: none;
  margin: 0;
  padding: 0;
}
.ops li {
  border-top: 1px solid var(--border);
}
.ops li:last-child {
  border-bottom: 1px solid var(--border);
}
.op-row {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 4px;
  border: 0;
  background: none;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.op-row:hover .op-label {
  color: var(--accent-text);
}
.op-label {
  flex: 1;
  min-width: 0;
}
.op-detail {
  display: grid;
  gap: 8px;
  padding: 0 4px 14px;
  justify-items: start;
}
.op-detail > :deep(.track) {
  justify-self: stretch;
}
.meta {
  font-size: 12.5px;
}
.err {
  color: var(--danger-text);
  font-size: 13px;
  overflow-wrap: anywhere;
}
.events li {
  display: grid;
  grid-template-columns: 112px 1fr;
  gap: 12px;
  padding: 6px 0;
  font-size: 13px;
  border-top: 1px solid var(--border);
}
.lvl-error .msg {
  color: var(--danger-text);
}
.lvl-warning .msg {
  color: var(--warning-text);
}
.msg {
  overflow-wrap: anywhere;
}
.msg .badge {
  margin-right: 6px;
}
.capture-link {
  font-size: 13px;
}
.lede {
  color: var(--text-2);
  font-size: 13.5px;
}
.lede .btn {
  margin-left: 8px;
}
@media (max-width: 640px) {
  .events li {
    grid-template-columns: 1fr;
    gap: 2px;
  }
}
</style>
