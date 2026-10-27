<script setup lang="ts">
import { computed, ref } from 'vue'
import { powerOff, powerOn, serverActions } from '../actions'
import ActionMenu from '../components/ActionMenu.vue'
import Icon from '../components/Icon.vue'
import StateBadge from '../components/StateBadge.vue'
import Sparkline from '../components/Sparkline.vue'
import StepTrack from '../components/StepTrack.vue'
import Tip from '../components/Tip.vue'
import { dateTime, isBusy, isOff, relativeTime, stateExplanation } from '../format'
import { api } from '../api'
import { errorText, operationsFor, runningOperation, store, toast } from '../store'
import type { Operation, Server } from '../types'
import { openServer, ui } from '../ui'

const filter = ref('')
const refreshing = ref(false)

const servers = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return store.servers
  return store.servers.filter((s) =>
    [s.id, s.name, s.description, s.bmc_host, s.os?.template_name, ...Object.values(s.metadata)]
      .filter(Boolean)
      .some((v) => v!.toLowerCase().includes(q)),
  )
})

const summary = computed(() => {
  const counts = { on: 0, busy: 0, trouble: 0 }
  for (const s of store.servers) {
    if (s.state.operation) counts.busy++
    else if (s.state.state === 'on') counts.on++
    else if (s.state.state === 'unreachable' || s.state.state === 'powered') counts.trouble++
  }
  return counts
})

/** What to show in the "Now" column when nothing is running */
function nextOrLast(server: Server): { scheduled?: Operation; failed?: Operation } {
  const ops = operationsFor(server.id)
  const scheduled = ops
    .filter((op) => op.status === 'scheduled')
    .sort((a, b) => (a.scheduled_for ?? '').localeCompare(b.scheduled_for ?? ''))[0]
  const last = ops.find((op) => op.status !== 'scheduled')
  const failed = last && (last.status === 'failed' || last.status === 'interrupted') ? last : undefined
  return { scheduled, failed }
}

function failedTip(server: Server) {
  const op = nextOrLast(server).failed!
  return `${op.label.replace(` ${server.name}`, '')} failed ${relativeTime(op.finished_at)}\n${op.error ?? ''}`.trim()
}

function scheduledTip(server: Server) {
  const op = nextOrLast(server).scheduled!
  return `${op.label.replace(` ${server.name}`, '')} scheduled for ${dateTime(op.scheduled_for)} (${relativeTime(op.scheduled_for)})`
}

function powerTip(server: Server) {
  const samples = store.power[server.id] ?? []
  const now = `${Math.round(server.state.watts!)} W now`
  if (samples.length < 2) return now
  const watts = samples.map(([, w]) => w)
  return `${now}\n${Math.round(Math.min(...watts))}–${Math.round(Math.max(...watts))} W over the last hour`
}

function bmcTitle(server: Server) {
  if (!server.has_bmc) return 'No BMC configured'
  if (server.state.bmc === 'ok') return `BMC ${server.bmc_host} responding`
  return `BMC ${server.bmc_host}: ${server.state.bmc_error ?? 'not responding'}`
}

async function refreshAll() {
  refreshing.value = true
  try {
    store.servers = await api.refreshAll()
  } catch (e) {
    toast(errorText(e), 'error')
  } finally {
    refreshing.value = false
  }
}

async function cancel(op: Operation) {
  try {
    await api.cancel(op.id)
  } catch (e) {
    toast(errorText(e), 'error')
  }
}
</script>

<template>
  <section class="view">
    <div class="toolbar">
      <div class="summary num">
        {{ store.servers.length }} servers
        <span class="dotsep">{{ summary.on }} on</span>
        <span v-if="summary.busy" class="dotsep accent">{{ summary.busy }} working</span>
        <span v-if="summary.trouble" class="dotsep warn">{{ summary.trouble }} need attention</span>
      </div>
      <div class="tools">
        <label class="search">
          <span class="visually-hidden">Filter servers</span>
          <input v-model="filter" class="input" type="search" placeholder="Filter by name, role, IP…" />
        </label>
        <button class="btn" :disabled="refreshing" @click="refreshAll">
          <Icon name="refresh" :class="{ spin: refreshing }" />
          {{ refreshing ? 'Checking…' : 'Check all now' }}
        </button>
      </div>
    </div>

    <div class="panel table-wrap" :class="{ compact: !!ui.serverId }">
      <table>
        <thead>
          <tr>
            <th>Server</th>
            <th>State</th>
            <th class="boot-col">Network boot</th>
            <th class="control-col">Control</th>
            <th class="power-col">Power</th>
            <th><span class="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody v-if="store.loaded">
          <tr
            v-for="server in servers"
            :key="server.id"
            :class="{ selected: ui.serverId === server.id }"
            @click="openServer(server.id)"
          >
            <td class="name-cell">
              <button class="row-link" @click.stop="openServer(server.id)">{{ server.name }}</button>
              <div class="sub">{{ server.description || server.metadata.role || server.id }}</div>
            </td>
            <td class="state-cell">
              <div class="state-line">
                <Tip :text="stateExplanation(server)">
                  <StateBadge :state="server.state.state" />
                </Tip>
                <Tip v-if="!runningOperation(server.id) && nextOrLast(server).failed" :text="failedTip(server)">
                  <button class="flag flag-failed" :aria-label="failedTip(server)" @click.stop="openServer(server.id, 'activity')">
                    <Icon name="alert" :size="14" />
                  </button>
                </Tip>
                <Tip v-if="nextOrLast(server).scheduled" :text="scheduledTip(server)">
                  <button class="flag" :aria-label="scheduledTip(server)" @click.stop="openServer(server.id)">
                    <Icon name="clock" :size="14" />
                  </button>
                </Tip>
              </div>
              <StepTrack v-if="runningOperation(server.id)" class="inline-track" :op="runningOperation(server.id)!" />
            </td>
            <td class="boot-cell">
              <template v-if="server.os">
                <div>{{ server.os.template_name || server.os.template_id }}</div>
                <span v-if="server.os.one_shot" class="badge tone-accent">Next boot only</span>
              </template>
              <span v-else class="muted">Local disk</span>
            </td>
            <td class="control-cell">
              <span class="link" :class="`link-${server.state.bmc}`" :title="bmcTitle(server)">
                <span class="pip" />BMC
              </span>
              <span
                v-for="o in server.state.outlets ?? []"
                :key="o.ref"
                class="link"
                :class="server.state.pdu === 'ok' ? `outlet-${o.state}` : 'link-unreachable'"
                :title="`Outlet ${o.ref}: ${server.state.pdu === 'ok' ? o.state : 'PDU not responding'}`"
              >
                <span class="pip" />{{ o.ref.split(':')[1] }}
              </span>
            </td>
            <td class="power-cell">
              <Tip v-if="server.state.watts != null" :text="powerTip(server)">
                <span class="watts num">{{ Math.round(server.state.watts) }} W</span>
                <Sparkline :samples="store.power[server.id] ?? []" />
              </Tip>
            </td>
            <td class="actions-cell" @click.stop>
              <button
                v-if="runningOperation(server.id)"
                class="btn btn-sm btn-ghost"
                @click="cancel(runningOperation(server.id)!)"
              >
                Cancel
              </button>
              <button v-else-if="isOff(server)" class="btn btn-sm" :disabled="isBusy(server)" @click="powerOn(server)">
                <Icon name="power" :size="14" /> Power on
              </button>
              <button
                v-else-if="server.state.state === 'on' || server.state.state === 'powered' || server.state.state === 'partial'"
                class="btn btn-sm"
                :disabled="isBusy(server)"
                @click="powerOff(server)"
              >
                <Icon name="power" :size="14" /> Power off
              </button>
              <button v-else class="btn btn-sm" :disabled="isBusy(server)" @click="powerOn(server)">
                <Icon name="power" :size="14" /> Power on
              </button>
              <ActionMenu :groups="serverActions(server)" :label="`More actions for ${server.name}`" />
            </td>
          </tr>
          <tr v-if="!servers.length" class="empty-row">
            <td colspan="6" class="empty">No servers match “{{ filter }}”.</td>
          </tr>
        </tbody>
        <tbody v-else>
          <tr v-for="i in 5" :key="i" class="skeleton-row">
            <td><div class="skeleton" style="width: 110px; height: 14px" /></td>
            <td><div class="skeleton" style="width: 80px; height: 14px" /></td>
            <td><div class="skeleton" style="width: 90px; height: 14px" /></td>
            <td><div class="skeleton" style="width: 60px; height: 14px" /></td>
            <td><div class="skeleton" style="width: 90px; height: 14px" /></td>
            <td />
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<style scoped>
.view {
  display: grid;
  gap: 16px;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
}
.summary {
  color: var(--text-2);
}
.dotsep::before {
  content: '·';
  margin: 0 8px;
  color: var(--text-3);
}
.accent {
  color: var(--accent-text);
}
.warn {
  color: var(--warning-text);
}
.tools {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.search .input {
  width: 240px;
}
.spin {
  animation: spin 1s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.table-wrap {
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-variant-numeric: tabular-nums;
}
thead th {
  position: sticky;
  top: 0;
  padding: 10px 16px;
  background: var(--surface-2);
  color: var(--text-2);
  font-size: 12.5px;
  font-weight: 500;
  text-align: left;
  white-space: nowrap;
}
thead th:first-child {
  border-top-left-radius: var(--r-lg);
}
thead th:last-child {
  border-top-right-radius: var(--r-lg);
}
tbody td {
  padding: 12px 16px;
  border-top: 1px solid var(--border);
  vertical-align: top;
}
tbody tr {
  cursor: pointer;
  transition: background-color 120ms var(--ease);
}
tbody tr:hover {
  background: color-mix(in srgb, var(--surface-2) 55%, transparent);
}
tbody tr.selected {
  background: var(--accent-soft);
}
.empty-row,
.skeleton-row {
  cursor: default;
}
.row-link {
  padding: 0;
  text-align: left;
  border: 0;
  background: none;
  color: var(--text);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
  border-radius: 4px;
}
.sub {
  margin-top: 2px;
  font-size: 12.5px;
  color: var(--text-2);
}
.name-cell {
  min-width: 140px;
}
.state-cell {
  min-width: 170px;
}
.state-line {
  display: flex;
  align-items: center;
  gap: 6px;
}
.flag {
  display: inline-grid;
  place-items: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: none;
  color: var(--text-2);
  cursor: pointer;
}
.flag:hover {
  background: var(--surface-2);
}
.flag-failed {
  color: var(--danger);
}
.inline-track {
  margin-top: 8px;
  max-width: 280px;
}
.power-cell {
  white-space: nowrap;
}
.watts {
  display: inline-block;
  min-width: 46px;
  font-size: 13px;
}
.boot-cell {
  min-width: 120px;
}
.boot-cell .badge {
  margin-top: 4px;
}
.control-cell {
  white-space: nowrap;
}
.link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-right: 10px;
  font-size: 12.5px;
  color: var(--text-2);
}
.pip {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-3);
}
.link-ok .pip,
.outlet-on .pip {
  background: var(--success);
}
.link-unreachable .pip {
  background: var(--danger);
}
.link-none {
  opacity: 0.45;
}
.outlet-off .pip {
  background: var(--neutral);
}
.actions-cell {
  white-space: nowrap;
  text-align: right;
  cursor: default;
}
.actions-cell > * {
  vertical-align: middle;
}
.actions-cell .btn {
  margin-right: 4px;
}

/* Beside the open panel: keep name, state, power and actions */
.compact .boot-col,
.compact .boot-cell,
.compact .control-col,
.compact .control-cell {
  display: none;
}

@media (max-width: 860px) {
  .boot-col,
  .boot-cell {
    display: none;
  }
}

/* Phones: each row becomes a stacked block */
@media (max-width: 640px) {
  .search .input {
    width: 100%;
  }
  .tools,
  .search {
    width: 100%;
  }
  .tools .btn {
    flex: none;
  }
  .search {
    flex: 1;
  }
  thead {
    display: none;
  }
  table,
  tbody,
  tr,
  td {
    display: block;
  }
  tbody tr {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 6px 12px;
    padding: 12px 16px;
    border-top: 1px solid var(--border);
  }
  tbody tr:first-child {
    border-top: 0;
  }
  tbody td {
    padding: 0;
    border: 0;
  }
  .name-cell {
    grid-column: 1;
  }
  .state-cell {
    grid-column: 2;
    text-align: right;
    min-width: 0;
  }
  .state-line {
    justify-content: flex-end;
  }
  .inline-track {
    margin-left: auto;
  }
  .power-cell {
    grid-column: 1;
  }
  .power-cell:empty {
    display: none;
  }
  .boot-cell {
    display: none;
  }
  .control-cell {
    grid-column: 1;
    align-self: center;
  }
  .actions-cell {
    grid-column: 2;
  }
}
</style>
