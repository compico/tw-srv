<script setup lang="ts">
import type { TicketStatus } from '../api'

defineProps<{ status: TicketStatus }>()

const text: Record<TicketStatus, string> = {
  open: 'Открыто',
  waiting: 'Ждёт ответа',
  closed: 'Закрыто',
}
</script>

<template>
  <span class="pill" :class="`t-${status}`">{{ text[status] }}</span>
</template>
