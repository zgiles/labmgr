<script setup lang="ts">
import type { Server } from '../types'

defineProps<{ server: Server }>()
</script>

<template>
  <p class="muted">The serial console for {{ server.name }} isn't available yet.</p>
</template>
