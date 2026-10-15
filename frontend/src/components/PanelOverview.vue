<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import { dateTime, relativeTime } from '../format'
import { errorText, operationsFor, toast } from '../store'
import type { BMCDetails, Operation, Server } from '../types'
import { setTab } from '../ui'
import Icon from './Icon.vue'
import StepTrack from './StepTrack.vue'

const props = defineProps<{ server: Server }>()

const ops = computed(() => operationsFor(props.server.id))
const running = computed(() => ops.value.filter((op) => op.status === 'running' || op.status === 'pending'))
const scheduled = computed(() =>
  ops.value
    .filter((op) => op.status === 'scheduled')
    .sort((a, b) => (a.scheduled_for ?? '').localeCompare(b.scheduled_for ?? '')),
)
const recent = computed(() => ops.value.filter((op) => !['running', 'pending', 'scheduled'].includes(op.status)).slice(0, 3))

const hw = ref<BMCDetails>()
const hwError = ref('')
const hwLoading = ref(false)

async function loadHardware() {
  if (!props.server.has_bmc || props.server.state.bmc !== 'ok') return
  hwLoading.value = true
  hwError.value = ''
  try {
    hw.value = await api.bmc(props.server.id)
  } catch (e) {
    hwError.value = errorText(e)
  } finally {
    hwLoading.value = false
  }
}
// Load when the panel opens, when it switches server, and when a dark BMC comes back
watch(
  () => [props.server.id, props.server.state.bmc] as const,
  ([id], old) => {
    if (old && old[0] !== id) hw.value = undefined
    if (!hw.value && !hwLoading.value) loadHardware()
  },
  { immediate: true },
)

const healthTone = computed(() => {
  const h = hw.value?.info.health
  return h === 'OK' ? 'success' : h === 'Warning' ? 'warning' : h ? 'danger' : ''
})

async function act(fn: () => Promise<unknown>) {
  try {
    await fn()
  } catch (e) {
    toast(errorText(e), 'error')
  }
}
const cancel = (op: Operation) => act(() => api.cancel(op.id))
const runNow = (op: Operation) => act(() => api.runNow(op.id))

const metadata = computed(() =>
  Object.entries(props.server.metadata).filter(([k]) => !['mac', 'hostname', 'role'].includes(k)),
)
</script>

<template>
  <div class="stack">
    <section v-for="op in running" :key="op.id" class="op-card panel">
      <div class="op-head">
        <strong>{{ op.label }}</strong>
        <button class="btn btn-sm" @click="cancel(op)">Cancel</button>
      </div>
      <StepTrack :op="op" detailed />
    </section>

    <section v-if="scheduled.length">
      <h3 class="section-title">Scheduled</h3>
      <ul class="list">
        <li v-for="op in scheduled" :key="op.id">
          <Icon name="clock" class="faint" />
          <div class="grow">
            <div>{{ op.label.replace(` ${server.name}`, '') }}</div>
            <div class="sub num">{{ dateTime(op.scheduled_for) }} · {{ relativeTime(op.scheduled_for) }}</div>
          </div>
          <button class="btn btn-sm btn-ghost" @click="runNow(op)">Run now</button>
          <button class="btn btn-sm btn-ghost" @click="cancel(op)">Cancel</button>
        </li>
      </ul>
    </section>

    <section>
      <h3 class="section-title">Connections</h3>
      <dl class="facts">
        <template v-if="server.has_bmc">
          <dt>BMC</dt>
          <dd>
            <a v-if="server.console.web_url" :href="server.console.web_url" target="_blank" rel="noopener" class="mono">
              {{ server.bmc_host }} <Icon name="external" :size="12" />
            </a>
            <div v-if="server.state.bmc === 'ok'" class="sub ok">Responding · checked {{ relativeTime(server.state.bmc_checked_at) }}</div>
            <div v-else-if="server.state.bmc === 'unreachable'" class="sub bad">{{ server.state.bmc_error ?? 'Not responding' }}</div>
          </dd>
        </template>
        <template v-if="server.outlets.length">
          <dt>Outlets</dt>
          <dd>
            <div v-for="o in server.state.outlets ?? []" :key="o.ref" class="outlet">
              <span class="mono">{{ o.ref }}</span>
              <span class="badge" :class="o.state === 'on' ? 'tone-success' : ''">{{ server.state.pdu === 'ok' ? o.state : 'unknown' }}</span>
            </div>
            <div v-if="server.state.pdu === 'unreachable'" class="sub bad">PDU: {{ server.state.pdu_error ?? 'not responding' }}</div>
          </dd>
        </template>
        <template v-if="server.mac">
          <dt>Boot MAC</dt>
          <dd class="mono">{{ server.mac }}</dd>
        </template>
        <dt>Hostname</dt>
        <dd>{{ server.metadata.hostname || server.name }}</dd>
        <template v-if="server.metadata.role">
          <dt>Role</dt>
          <dd>{{ server.metadata.role }}</dd>
        </template>
        <template v-if="server.description">
          <dt>Description</dt>
          <dd>{{ server.description }}</dd>
        </template>
        <template v-for="[k, v] in metadata" :key="k">
          <dt>{{ k }}</dt>
          <dd :class="{ mono: k.includes('path') || k.includes('template') }">{{ v }}</dd>
        </template>
      </dl>
    </section>

    <section v-if="server.has_bmc">
      <h3 class="section-title">Hardware</h3>
      <div v-if="hwLoading && !hw" class="skel">
        <div class="skeleton" style="height: 14px; width: 60%" />
        <div class="skeleton" style="height: 14px; width: 40%" />
      </div>
      <p v-else-if="server.state.bmc !== 'ok'" class="muted">Available once the BMC responds.</p>
      <p v-else-if="hwError" class="muted">Couldn't read hardware details: {{ hwError }}</p>
      <dl v-else-if="hw" class="facts">
        <dt>Model</dt>
        <dd>{{ [hw.info.manufacturer, hw.info.model].filter(Boolean).join(' ') || '—' }}</dd>
        <template v-if="hw.info.serial_number">
          <dt>Serial</dt>
          <dd class="mono">{{ hw.info.serial_number }}</dd>
        </template>
        <template v-if="hw.info.health">
          <dt>Health</dt>
          <dd>
            <span class="badge" :class="`tone-${healthTone}`">{{ hw.info.health }}</span>
            <button v-if="healthTone && healthTone !== 'success'" class="linkish" @click="setTab('activity')">See BMC log</button>
          </dd>
        </template>
        <template v-if="hw.info.bios_version">
          <dt>BIOS</dt>
          <dd>{{ hw.info.bios_version }}</dd>
        </template>
        <template v-if="hw.info.bmc_model || hw.info.bmc_firmware">
          <dt>BMC</dt>
          <dd>{{ [hw.info.bmc_model, hw.info.bmc_firmware].filter(Boolean).join(' · ') }}</dd>
        </template>
      </dl>
    </section>

    <section v-if="recent.length">
      <h3 class="section-title">Recent</h3>
      <ul class="list">
        <li v-for="op in recent" :key="op.id">
          <Icon :name="op.status === 'succeeded' ? 'check' : 'x'" :class="op.status === 'succeeded' ? 'ok' : 'bad'" />
          <div class="grow">
            <div>{{ op.label.replace(` ${server.name}`, '') }}</div>
            <div class="sub" :class="{ bad: op.status !== 'succeeded' }">
              {{ op.status === 'succeeded' ? op.note || 'Done' : op.error }} · {{ relativeTime(op.finished_at ?? op.created_at) }}
            </div>
          </div>
        </li>
      </ul>
      <button class="linkish" @click="setTab('activity')">All activity</button>
    </section>
  </div>
</template>

<style scoped>
.stack {
  display: grid;
  gap: 28px;
}
.op-card {
  padding: 16px 18px;
}
.op-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 4px;
}
.list li {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 6px 0;
}
.list li > svg {
  margin-top: 3px;
  flex: none;
}
.grow {
  flex: 1;
  min-width: 0;
}
.sub {
  font-size: 12.5px;
  color: var(--text-2);
  overflow-wrap: anywhere;
}
.ok {
  color: var(--success-text);
}
.bad {
  color: var(--danger-text);
}
.outlet {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}
.skel {
  display: grid;
  gap: 8px;
}
.linkish {
  padding: 0;
  margin-left: 8px;
  border: 0;
  background: none;
  color: var(--accent-text);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
section > .linkish {
  margin: 6px 0 0;
}
</style>
