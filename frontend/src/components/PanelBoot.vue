<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import { dateTime, isBusy, relativeTime } from '../format'
import { errorText, runOperation, store, toast } from '../store'
import type { BootScript, Server } from '../types'
import { openDialog, showView } from '../ui'

const props = defineProps<{ server: Server }>()

// Network boot assignment
const choice = ref('')
const nextOnly = ref(false)
const saving = ref(false)
watch(
  () => [props.server.id, props.server.os?.template_id, props.server.os?.one_shot] as const,
  () => {
    choice.value = props.server.os?.one_shot ? (props.server.os.then_template ?? '') : (props.server.os?.template_id ?? '')
    nextOnly.value = false
  },
  { immediate: true },
)
const currentPersistent = computed(() =>
  props.server.os?.one_shot ? (props.server.os.then_template ?? '') : (props.server.os?.template_id ?? ''),
)
const dirty = computed(() => nextOnly.value || choice.value !== currentPersistent.value)

async function saveAssignment() {
  saving.value = true
  try {
    if (!choice.value && !nextOnly.value) await api.clearOS(props.server.id)
    else await api.assignOS(props.server.id, choice.value || 'local-disk', nextOnly.value)
    toast(nextOnly.value ? `${props.server.name} will use this image on its next network boot only` : 'Network boot updated')
    loadScript()
  } catch (e) {
    toast(errorText(e), 'error')
  } finally {
    saving.value = false
  }
}

// BMC boot device
const bootOverride = ref<{ target: string; enabled: string } | null>(null)
const overrideError = ref('')
async function loadOverride() {
  bootOverride.value = null
  overrideError.value = ''
  if (props.server.state.bmc !== 'ok') return
  try {
    const details = await api.bmc(props.server.id)
    bootOverride.value = details.boot
    if (details.boot_error) overrideError.value = details.boot_error
  } catch (e) {
    overrideError.value = errorText(e)
  }
}
async function setOverride(target: string) {
  const op = await runOperation(props.server.id, { kind: 'boot_override', target })
  if (op) setTimeout(loadOverride, 2500)
}
const overrideText = computed(() => {
  const b = bootOverride.value
  if (!b) return ''
  if (b.enabled === 'Disabled' || !b.target || b.target === 'none') return 'Normal boot order'
  const names: Record<string, string> = { pxe: 'Network (PXE)', bios: 'BIOS setup', disk: 'Local disk', cd: 'Virtual CD' }
  return `${names[b.target] ?? b.target}, ${b.enabled === 'Once' ? 'next boot only' : 'every boot'}`
})

// Script preview
const script = ref<BootScript>()
const scriptError = ref('')
async function loadScript() {
  scriptError.value = ''
  try {
    script.value = await api.bootScript(props.server.id)
  } catch (e) {
    scriptError.value = errorText(e)
  }
}

watch(
  () => props.server.id,
  () => {
    loadOverride()
    loadScript()
  },
  { immediate: true },
)
watch(
  () => props.server.state.bmc,
  (now, before) => {
    if (now === 'ok' && before !== 'ok') loadOverride()
  },
)
</script>

<template>
  <div class="stack">
    <section>
      <h3 class="section-title">When it network boots</h3>
      <p class="lede">
        Lab Manager answers {{ server.name }}'s iPXE request with this image.
        <template v-if="!server.mac">
          <br /><span class="warn-text">There's no <span class="mono">metadata.mac</span> for this server, so it can't be matched yet.</span>
        </template>
      </p>
      <div v-if="server.os?.one_shot" class="notice">
        Next network boot only: <strong>{{ server.os.template_name }}</strong>.
        After that, {{ server.os.then_template ? store.templates.find((t) => t.id === server.os?.then_template)?.name ?? server.os.then_template : 'local disk' }}.
      </div>
      <div class="assign">
        <label class="field grow">
          <span class="visually-hidden">Boot image</span>
          <select v-model="choice" class="select">
            <option value="">Local disk (default)</option>
            <option v-for="t in store.templates.filter((t) => t.id !== 'local-disk')" :key="t.id" :value="t.id">{{ t.name }}</option>
          </select>
        </label>
        <button class="btn btn-primary" :disabled="!dirty || saving" @click="saveAssignment">Save</button>
      </div>
      <label class="checkbox">
        <input v-model="nextOnly" type="checkbox" />
        Next network boot only, then go back
      </label>
      <p class="hint">
        <button class="linkish" @click="showView('boot')">Manage boot images</button>
      </p>
      <dl class="facts last">
        <dt>Last network boot</dt>
        <dd v-if="server.state.last_boot">
          {{ relativeTime(server.state.last_boot.at) }} ({{ dateTime(server.state.last_boot.at) }}) from
          <span class="mono">{{ server.state.last_boot.client_ip }}</span>
        </dd>
        <dd v-else class="muted">Not since Lab Manager started</dd>
      </dl>
    </section>

    <section v-if="server.has_bmc">
      <h3 class="section-title">Boot device</h3>
      <p class="lede">Set through the BMC. Takes effect the next time the server starts.</p>
      <p v-if="server.state.bmc !== 'ok'" class="muted">Available once the BMC responds.</p>
      <template v-else>
        <p class="current">
          <span class="muted">Currently:</span>
          <span v-if="bootOverride">{{ overrideText }}</span>
          <span v-else-if="overrideError" class="warn-text">{{ overrideError }}</span>
          <span v-else class="skeleton" style="display: inline-block; width: 120px; height: 12px" />
        </p>
        <div class="buttons">
          <button class="btn btn-sm" :disabled="isBusy(server)" @click="setOverride('pxe')">Network once</button>
          <button class="btn btn-sm" :disabled="isBusy(server)" @click="setOverride('bios')">BIOS setup once</button>
          <button class="btn btn-sm" :disabled="isBusy(server)" @click="setOverride('disk')">Disk once</button>
          <button class="btn btn-sm btn-ghost" :disabled="isBusy(server)" @click="setOverride('none')">Clear</button>
        </div>
      </template>
    </section>

    <section v-if="server.has_bmc">
      <h3 class="section-title">Reinstall</h3>
      <p class="lede">Network boot an installer once, without changing what this server boots afterwards.</p>
      <button class="btn" :disabled="isBusy(server)" @click="openDialog({ type: 'reinstall', serverId: server.id })">Reinstall {{ server.name }}…</button>
    </section>

    <section>
      <div class="script-head">
        <h3 class="section-title">Boot script</h3>
        <button class="btn btn-sm btn-ghost" @click="loadScript">Refresh</button>
      </div>
      <p v-if="script" class="lede">What {{ server.name }} gets right now: <strong>{{ script.source_name }}</strong><span v-if="script.one_shot"> (once)</span>.</p>
      <p v-if="scriptError" class="warn-text">{{ scriptError }}</p>
      <pre v-if="script" class="code">{{ script.script }}</pre>
      <div v-else-if="!scriptError" class="skeleton" style="height: 120px" />
    </section>
  </div>
</template>

<style scoped>
.stack {
  display: grid;
  gap: 32px;
}
.lede {
  margin: -4px 0 12px;
  color: var(--text-2);
  font-size: 13.5px;
}
.assign {
  display: flex;
  gap: 8px;
  margin-bottom: 10px;
}
.grow {
  flex: 1;
}
.notice {
  margin-bottom: 12px;
  padding: 10px 14px;
  border-radius: var(--r-md);
  background: var(--accent-soft);
  color: var(--accent-text);
  font-size: 13.5px;
}
.hint {
  margin-top: 8px;
}
.linkish {
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent-text);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
.last {
  margin-top: 16px;
}
.current {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.warn-text {
  color: var(--warning-text);
}
.script-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}
</style>
