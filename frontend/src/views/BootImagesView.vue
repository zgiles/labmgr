<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'
import Icon from '../components/Icon.vue'
import TemplateDialog from '../components/TemplateDialog.vue'
import { errorText, reloadTemplates, store, toast } from '../store'
import type { OSTemplate } from '../types'
import { confirmAction, openServer } from '../ui'

const editing = ref<OSTemplate | 'new' | null>(null)

const methodLabel: Record<string, string> = { chain: 'Chain', template: 'iPXE script', local: 'Local disk' }

const templates = computed(() =>
  [...store.templates].sort((a, b) => Number(b.is_built_in) - Number(a.is_built_in) || a.name.localeCompare(b.name)),
)

/** Servers that boot (or will next boot) each image */
function usedBy(id: string) {
  return store.servers.filter((s) => s.os?.template_id === id || s.os?.then_template === id)
}

async function remove(t: OSTemplate) {
  const ok = await confirmAction({
    title: `Delete ${t.name}?`,
    body: 'Servers can no longer be set to network boot this image. This can’t be undone.',
    confirmLabel: 'Delete boot image',
    danger: true,
  })
  if (!ok) return
  try {
    await api.deleteTemplate(t.id)
    await reloadTemplates()
    toast(`Deleted ${t.name}`)
  } catch (e) {
    toast(errorText(e), 'error')
  }
}
</script>

<template>
  <section class="view">
    <div class="toolbar">
      <p class="muted intro">
        What a server gets when it network boots. Assign one per server on its Boot tab, or use Reinstall to boot one once.
      </p>
      <button class="btn btn-primary" @click="editing = 'new'"><Icon name="plus" /> New boot image</button>
    </div>

    <div class="panel table-wrap">
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Method</th>
            <th>Target</th>
            <th>Used by</th>
            <th><span class="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in templates" :key="t.id" @click="editing = t">
            <td>
              <div class="name">{{ t.name }}</div>
              <div class="sub">{{ t.description || t.id }}</div>
            </td>
            <td><span class="badge">{{ methodLabel[t.boot_method] ?? t.boot_method }}</span></td>
            <td class="target">
              <span v-if="t.next_path" class="mono" :title="t.next_path">{{ t.next_path }}</span>
              <span v-else-if="t.boot_template" class="muted">{{ t.boot_template.split('\n').filter((l) => l.trim() && !l.startsWith('#')).length }} lines of iPXE</span>
              <span v-else class="faint">—</span>
            </td>
            <td class="used" @click.stop>
              <button v-for="s in usedBy(t.id)" :key="s.id" class="chip" @click="openServer(s.id, 'boot')">{{ s.name }}</button>
              <span v-if="!usedBy(t.id).length" class="faint">—</span>
            </td>
            <td class="actions" @click.stop>
              <span v-if="t.is_built_in" class="faint">Built in</span>
              <template v-else>
                <button class="btn btn-sm btn-ghost" @click="editing = t">Edit</button>
                <button class="btn btn-sm btn-ghost danger" :disabled="usedBy(t.id).length > 0" :title="usedBy(t.id).length ? 'Still assigned to servers' : ''" @click="remove(t)">
                  Delete
                </button>
              </template>
            </td>
          </tr>
          <tr v-if="templates.length <= 1" class="hint-row">
            <td colspan="5" class="empty">
              Add an installer or live image (a chain URL like netboot.xyz, or your own iPXE script) to start reinstalling servers from here.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <TemplateDialog v-if="editing" :template="editing === 'new' ? undefined : editing" @close="editing = null" />
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
  gap: 16px;
  flex-wrap: wrap;
}
.intro {
  max-width: 64ch;
}
.table-wrap {
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
}
th {
  padding: 10px 16px;
  background: var(--surface-2);
  color: var(--text-2);
  font-size: 12.5px;
  font-weight: 500;
  text-align: left;
}
th:first-child {
  border-top-left-radius: var(--r-lg);
}
th:last-child {
  border-top-right-radius: var(--r-lg);
}
td {
  padding: 12px 16px;
  border-top: 1px solid var(--border);
  vertical-align: top;
}
tbody tr {
  cursor: pointer;
}
tbody tr:hover {
  background: color-mix(in srgb, var(--surface-2) 55%, transparent);
}
.hint-row {
  cursor: default;
}
.hint-row:hover {
  background: none;
}
.name {
  font-weight: 600;
}
.sub {
  font-size: 12.5px;
  color: var(--text-2);
}
.target {
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.used {
  cursor: default;
}
.chip {
  margin: 0 4px 4px 0;
  padding: 2px 8px;
  border: 1px solid var(--border-strong);
  border-radius: var(--r-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 12.5px;
  cursor: pointer;
}
.chip:hover {
  border-color: var(--accent);
  color: var(--accent-text);
}
.actions {
  text-align: right;
  white-space: nowrap;
  cursor: default;
}
.danger:not(:disabled) {
  color: var(--danger-text);
}
</style>
