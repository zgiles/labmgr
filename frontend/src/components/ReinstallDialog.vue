<script setup lang="ts">
import { computed, ref } from 'vue'
import { runOperation, store } from '../store'
import type { Server } from '../types'
import { setTab } from '../ui'
import Dialog from './Dialog.vue'

const props = defineProps<{ server: Server }>()
const emit = defineEmits<{ close: [] }>()

const installable = computed(() => store.templates.filter((t) => t.boot_method !== 'local'))
const templateId = ref(installable.value[0]?.id ?? '')
const chosen = computed(() => store.templates.find((t) => t.id === templateId.value))
const confirmName = ref('')
const saving = ref(false)

const afterwards = computed(() => props.server.os?.template_name ?? 'local disk')
const ready = computed(() => !!chosen.value && confirmName.value.trim() === props.server.name && !!props.server.mac)

async function start() {
  if (!ready.value) return
  saving.value = true
  const op = await runOperation(props.server.id, { kind: 'reinstall', target: templateId.value })
  saving.value = false
  if (op) {
    setTab('overview')
    emit('close')
  }
}
</script>

<template>
  <Dialog :title="`Reinstall ${server.name}`" wide @close="emit('close')">
    <div class="form">
      <p v-if="!server.mac" class="warn">
        {{ server.name }} has no <span class="mono">metadata.mac</span> in the config, so Lab Manager can't recognise it when it network boots. Add one first.
      </p>
      <p v-else-if="!installable.length" class="warn">
        There are no network boot images yet. Add one under Boot images first.
      </p>

      <label class="field">
        <span>Boot image</span>
        <select v-model="templateId" class="select" :disabled="!installable.length">
          <option v-for="t in installable" :key="t.id" :value="t.id">{{ t.name }}</option>
        </select>
        <span v-if="chosen?.description" class="hint">{{ chosen.description }}</span>
      </label>

      <ol class="plan">
        <li>Serve <strong>{{ chosen?.name ?? '…' }}</strong> on {{ server.name }}'s next network boot only.</li>
        <li>Tell the BMC to boot from the network once.</li>
        <li>{{ server.state.state === 'on' ? 'Hard reset the server.' : 'Power the server on.' }}</li>
        <li>Wait until it fetches its boot script. After that it boots from <strong>{{ afterwards }}</strong> again.</li>
      </ol>

      <label class="field">
        <span>Type <span class="mono">{{ server.name }}</span> to confirm</span>
        <input v-model="confirmName" class="input mono" autocomplete="off" spellcheck="false" @keydown.enter="start" />
      </label>
    </div>
    <template #footer>
      <button class="btn" @click="emit('close')">Cancel</button>
      <button class="btn btn-danger" :disabled="!ready || saving" @click="start">Reinstall {{ server.name }}</button>
    </template>
  </Dialog>
</template>

<style scoped>
.form {
  display: grid;
  gap: 18px;
  color: var(--text);
}
.plan {
  margin: 0;
  padding: 14px 16px 14px 34px;
  display: grid;
  gap: 6px;
  border-radius: var(--r-md);
  background: var(--surface-2);
  color: var(--text-2);
}
.plan strong {
  color: var(--text);
  font-weight: 600;
}
.warn {
  padding: 10px 14px;
  border-radius: var(--r-md);
  background: var(--warning-soft);
  color: var(--warning-text);
}
</style>
