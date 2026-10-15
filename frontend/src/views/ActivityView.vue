<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'
import StepTrack from '../components/StepTrack.vue'
import { actorName, dateTime, opStatusInfo, relativeTime } from '../format'
import { activeOperations, errorText, operationsNewestFirst, serverById, store, toast } from '../store'
import type { Operation } from '../types'
import { openServer } from '../ui'

const running = computed(() => activeOperations.value.filter((op) => op.status !== 'scheduled'))
const scheduled = computed(() =>
  activeOperations.value
    .filter((op) => op.status === 'scheduled')
    .sort((a, b) => (a.scheduled_for ?? '').localeCompare(b.scheduled_for ?? '')),
)
const finished = computed(() => operationsNewestFirst.value.filter((op) => !activeOperations.value.includes(op)).slice(0, 40))
const expanded = ref<string | null>(null)

async function act(fn: () => Promise<unknown>) {
  try {
    await fn()
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

const cancel = (op: Operation) => act(() => api.cancel(op.id))
const runNow = (op: Operation) => act(() => api.runNow(op.id))
</script>

<template>
  <section class="view">
    <div class="columns">
      <div class="col">
        <section>
          <h2 class="heading">Running</h2>
          <p v-if="!running.length" class="muted">Nothing is running.</p>
          <div v-for="op in running" :key="op.id" class="panel card">
            <div class="card-head">
              <button class="link" @click="openServer(op.server_id ?? '')">{{ op.label }}</button>
              <button class="btn btn-sm" @click="cancel(op)">Cancel</button>
            </div>
            <StepTrack :op="op" detailed />
          </div>
        </section>

        <section>
          <h2 class="heading">Scheduled</h2>
          <p v-if="!scheduled.length" class="muted">Nothing scheduled. Use Schedule… in a server's menu.</p>
          <ul class="rows">
            <li v-for="op in scheduled" :key="op.id">
              <div class="grow">
                <button class="link" @click="openServer(op.server_id ?? '')">{{ op.label }}</button>
                <div class="sub num">{{ dateTime(op.scheduled_for) }} · {{ relativeTime(op.scheduled_for) }}</div>
              </div>
              <button class="btn btn-sm btn-ghost" @click="runNow(op)">Run now</button>
              <button class="btn btn-sm btn-ghost" @click="cancel(op)">Cancel</button>
            </li>
          </ul>
        </section>

        <section>
          <h2 class="heading">Finished</h2>
          <p v-if="!finished.length" class="muted">No operations yet.</p>
          <ul class="rows">
            <li v-for="op in finished" :key="op.id" class="finished" :class="{ open: expanded === op.id }">
              <button class="row-btn" :aria-expanded="expanded === op.id" @click="expanded = expanded === op.id ? null : op.id">
                <span class="grow">
                  {{ op.label }}
                  <span class="sub block">{{ relativeTime(op.finished_at ?? op.created_at) }}<template v-if="op.requested_by"> · {{ actorName(op.requested_by) }}</template></span>
                </span>
                <span class="badge" :class="`tone-${opStatusInfo[op.status].tone}`">{{ opStatusInfo[op.status].label }}</span>
              </button>
              <div v-if="expanded === op.id" class="detail">
                <p v-if="op.error && !op.steps.some((st) => st.error === op.error)" class="err">{{ op.error }}</p>
                <p v-if="op.note" class="muted">{{ op.note }}</p>
                <StepTrack v-if="op.steps.length" :op="op" detailed />
              </div>
            </li>
          </ul>
        </section>
      </div>

      <aside class="col">
        <h2 class="heading">History</h2>
        <ul class="events panel">
          <li v-for="e in store.events" :key="e.id" :class="`lvl-${e.level ?? 'info'}`">
            <div class="ev-head">
              <button v-if="e.server_id" class="link small" @click="openServer(e.server_id)">{{ serverById(e.server_id)?.name ?? e.server_id }}</button>
              <span class="faint num" :title="dateTime(e.at)">{{ relativeTime(e.at) }}</span>
            </div>
            <div class="msg">{{ e.message }}<span v-if="e.actor" class="faint"> · {{ actorName(e.actor) }}</span></div>
          </li>
          <li v-if="!store.events.length" class="muted">Nothing recorded yet.</li>
        </ul>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.columns {
  display: grid;
  grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr);
  gap: 32px;
  align-items: start;
}
.col {
  display: grid;
  gap: 32px;
}
aside.col {
  gap: 12px;
}
.heading {
  margin-bottom: 12px;
  font-size: 15px;
  font-weight: 600;
}
.card {
  padding: 16px 18px;
  margin-bottom: 10px;
}
.card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--text);
  font: inherit;
  font-weight: 500;
  text-align: left;
  cursor: pointer;
}
.link:hover {
  color: var(--accent-text);
}
.link.small {
  font-size: 12.5px;
}
.rows {
  list-style: none;
  margin: 0;
  padding: 0;
}
.rows li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  border-top: 1px solid var(--border);
}
.rows li.finished {
  display: block;
}
.row-btn {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 0;
  border: 0;
  background: none;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.grow {
  flex: 1;
  min-width: 0;
}
.sub {
  font-size: 12.5px;
  color: var(--text-2);
}
.block {
  display: block;
}
.detail {
  display: grid;
  gap: 8px;
  padding: 10px 0 4px;
}
.err {
  color: var(--danger-text);
  font-size: 13px;
  overflow-wrap: anywhere;
}
.events {
  list-style: none;
  margin: 0;
  padding: 4px 16px;
  max-height: calc(100vh - 200px);
  overflow-y: auto;
}
.events li {
  padding: 10px 0;
  border-top: 1px solid var(--border);
  font-size: 13px;
}
.events li:first-child {
  border-top: 0;
}
.ev-head {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 2px;
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
@media (max-width: 900px) {
  .columns {
    grid-template-columns: 1fr;
  }
  .events {
    max-height: none;
  }
}
</style>
