<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { api } from '../api'
import { errorText, reloadTemplates, toast } from '../store'
import type { OSTemplate } from '../types'
import Dialog from './Dialog.vue'

const props = defineProps<{ template?: OSTemplate }>()
const emit = defineEmits<{ close: [] }>()

const readOnly = computed(() => !!props.template?.is_built_in)
const form = reactive({
  name: props.template?.name ?? '',
  description: props.template?.description ?? '',
  boot_method: props.template?.boot_method ?? 'chain',
  next_path: props.template?.next_path ?? '',
  boot_template: props.template?.boot_template ?? '#!ipxe\n\n',
})
const error = ref('')
const saving = ref(false)
const touched = ref(false)

const problems = computed(() => ({
  name: !form.name.trim() ? 'Give it a name.' : '',
  next_path: form.boot_method === 'chain' && !form.next_path.trim() ? 'Enter the URL to chain to.' : '',
  boot_template:
    form.boot_method === 'template' && form.boot_template.replace('#!ipxe', '').trim() === ''
      ? 'Write the iPXE script.'
      : '',
}))
const valid = computed(() => !Object.values(problems.value).some(Boolean))

async function save() {
  touched.value = true
  if (!valid.value) return
  saving.value = true
  error.value = ''
  const body = {
    name: form.name.trim(),
    description: form.description.trim(),
    boot_method: form.boot_method,
    next_path: form.boot_method === 'chain' ? form.next_path.trim() : '',
    boot_template: form.boot_method === 'template' ? form.boot_template : '',
  }
  try {
    if (props.template) await api.updateTemplate(props.template.id, body)
    else await api.createTemplate(body)
    await reloadTemplates()
    toast(props.template ? 'Boot image saved' : `Added ${body.name}`)
    emit('close')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    saving.value = false
  }
}

// Shown as {{.MAC}} etc.; built here because braces can't appear inside a template mustache
const variables = ['.MAC', '.Hostname', '.Role', '.ServerName', '.ServerID', '.IncomingServerIP', 'index .Metadata "key"'].map(
  (v) => '{' + '{' + v + '}' + '}',
)
</script>

<template>
  <Dialog :title="template ? (readOnly ? template.name : `Edit ${template.name}`) : 'New boot image'" wide @close="emit('close')">
    <form class="form" @submit.prevent="save">
      <p v-if="readOnly" class="muted">Built-in images can't be changed.</p>
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" class="input" :class="{ invalid: touched && problems.name }" :disabled="readOnly" placeholder="Debian 13 installer" />
        <span v-if="touched && problems.name" class="error-text">{{ problems.name }}</span>
      </label>
      <label class="field">
        <span>Description</span>
        <input v-model="form.description" class="input" :disabled="readOnly" placeholder="What it installs or runs" />
      </label>
      <fieldset class="methods" :disabled="readOnly">
        <legend class="field-label">How it boots</legend>
        <label class="method" :class="{ on: form.boot_method === 'chain' }">
          <input v-model="form.boot_method" type="radio" value="chain" />
          <span><strong>Chain to a URL</strong><span class="muted">Hand off to another iPXE script, e.g. netboot.xyz</span></span>
        </label>
        <label class="method" :class="{ on: form.boot_method === 'template' }">
          <input v-model="form.boot_method" type="radio" value="template" />
          <span><strong>iPXE script</strong><span class="muted">Kernel, initrd and arguments written here</span></span>
        </label>
        <label class="method" :class="{ on: form.boot_method === 'local' }">
          <input v-model="form.boot_method" type="radio" value="local" />
          <span><strong>Local disk</strong><span class="muted">Boot the installed system</span></span>
        </label>
      </fieldset>

      <label v-if="form.boot_method === 'chain'" class="field">
        <span>URL</span>
        <input
          v-model="form.next_path"
          class="input mono"
          :class="{ invalid: touched && problems.next_path }"
          :disabled="readOnly"
          spellcheck="false"
          placeholder="http://boot.lab/{{.Hostname}}.ipxe"
        />
        <span v-if="touched && problems.next_path" class="error-text">{{ problems.next_path }}</span>
      </label>
      <label v-if="form.boot_method === 'template'" class="field">
        <span>Script</span>
        <textarea
          v-model="form.boot_template"
          class="textarea"
          :class="{ invalid: touched && problems.boot_template }"
          :disabled="readOnly"
          rows="12"
          spellcheck="false"
        />
        <span v-if="touched && problems.boot_template" class="error-text">{{ problems.boot_template }}</span>
      </label>
      <p v-if="form.boot_method !== 'local'" class="vars">
        Go template variables:
        <code v-for="v in variables" :key="v" class="mono">{{ v }}</code>
      </p>
      <p v-if="error" class="error-text">{{ error }}</p>
    </form>
    <template #footer>
      <button class="btn" @click="emit('close')">{{ readOnly ? 'Close' : 'Cancel' }}</button>
      <button v-if="!readOnly" class="btn btn-primary" :disabled="saving" @click="save">
        {{ template ? 'Save changes' : 'Add boot image' }}
      </button>
    </template>
  </Dialog>
</template>

<style scoped>
.form {
  display: grid;
  gap: 16px;
  color: var(--text);
}
.methods {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  border: 0;
}
.field-label {
  margin-bottom: 6px;
  padding: 0;
  font-size: 13px;
  font-weight: 500;
}
.method {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--r-md);
  cursor: pointer;
  transition: border-color 150ms var(--ease), background-color 150ms var(--ease);
}
.method.on {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.method input {
  margin-top: 3px;
  accent-color: var(--accent);
}
.method > span {
  display: grid;
  font-size: 13.5px;
}
.method strong {
  font-weight: 500;
}
.vars {
  font-size: 12.5px;
  color: var(--text-2);
  line-height: 2;
}
.vars code {
  margin-left: 6px;
  padding: 2px 6px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  color: var(--text);
  white-space: nowrap;
}
.error-text {
  color: var(--danger-text);
  font-size: 13px;
}
</style>
