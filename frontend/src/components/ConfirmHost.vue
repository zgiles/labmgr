<script setup lang="ts">
import { ui } from '../ui'
import Dialog from './Dialog.vue'

function answer(ok: boolean) {
  const request = ui.confirm
  ui.confirm = null
  request?.resolve(ok)
}
</script>

<template>
  <Dialog v-if="ui.confirm" :title="ui.confirm.title" @close="answer(false)">
    <p>{{ ui.confirm.body }}</p>
    <template #footer>
      <button class="btn" @click="answer(false)">Cancel</button>
      <button class="btn" :class="ui.confirm.danger ? 'btn-danger' : 'btn-primary'" @click="answer(true)">
        {{ ui.confirm.confirmLabel }}
      </button>
    </template>
  </Dialog>
</template>
