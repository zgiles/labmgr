<script setup lang="ts">
import { computed, ref } from 'vue'
import { dateTime, relativeTime } from '../format'
import { runOperation } from '../store'
import type { Server } from '../types'
import Dialog from './Dialog.vue'

const props = defineProps<{ server: Server }>()
const emit = defineEmits<{ close: [] }>()

const kinds = computed(() => {
  const list = [
    { value: 'power_off', label: 'Power off' },
    { value: 'power_on', label: 'Power on' },
  ]
  if (props.server.has_bmc) {
    list.splice(1, 0, { value: 'graceful_shutdown', label: 'Shut down (clean)' })
    list.push({ value: 'restart', label: 'Restart' })
  }
  return list
})

const kind = ref(props.server.state.state === 'off' ? 'power_on' : props.server.has_bmc ? 'graceful_shutdown' : 'power_off')

// datetime-local wants local time without a zone
function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const when = ref(toLocalInput(new Date(Date.now() + 3600_000)))
const whenDate = computed(() => new Date(when.value))
const valid = computed(() => !isNaN(whenDate.value.getTime()) && whenDate.value.getTime() > Date.now())
const saving = ref(false)

function inHours(h: number) {
  when.value = toLocalInput(new Date(Date.now() + h * 3600_000))
}
function tonightAt(hour: number) {
  const d = new Date()
  d.setHours(hour, 0, 0, 0)
  if (d.getTime() <= Date.now()) d.setDate(d.getDate() + 1)
  when.value = toLocalInput(d)
}

async function save() {
  if (!valid.value) return
  saving.value = true
  const op = await runOperation(props.server.id, { kind: kind.value, scheduled_for: whenDate.value.toISOString() })
  saving.value = false
  if (op) emit('close')
}
</script>

<template>
  <Dialog :title="`Schedule for ${server.name}`" @close="emit('close')">
    <form class="form" @submit.prevent="save">
      <label class="field">
        <span>Action</span>
        <select v-model="kind" class="select">
          <option v-for="k in kinds" :key="k.value" :value="k.value">{{ k.label }}</option>
        </select>
      </label>
      <label class="field">
        <span>When</span>
        <input v-model="when" type="datetime-local" class="input num" :class="{ invalid: !valid }" required />
        <span v-if="!valid" class="error-text">Pick a time in the future.</span>
        <span v-else class="hint">{{ dateTime(whenDate.toISOString()) }}, {{ relativeTime(whenDate.toISOString()) }}</span>
      </label>
      <div class="chips">
        <button type="button" class="btn btn-sm" @click="inHours(1)">In 1 hour</button>
        <button type="button" class="btn btn-sm" @click="inHours(4)">In 4 hours</button>
        <button type="button" class="btn btn-sm" @click="tonightAt(22)">10 pm</button>
        <button type="button" class="btn btn-sm" @click="tonightAt(7)">7 am</button>
      </div>
      <p class="note">The steps are planned when it runs, from the server's state at that moment.</p>
    </form>
    <template #footer>
      <button class="btn" @click="emit('close')">Cancel</button>
      <button class="btn btn-primary" :disabled="!valid || saving" @click="save">Schedule</button>
    </template>
  </Dialog>
</template>

<style scoped>
.form {
  display: grid;
  gap: 16px;
  color: var(--text);
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.note {
  font-size: 12.5px;
  color: var(--text-2);
}
</style>
