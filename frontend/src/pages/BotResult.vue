<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

const status = computed(() => String(useRoute().query.status ?? ''))
const ok = computed(() => status.value === 'connected')
const texts: Record<string, string> = {
  connected: 'Ваш бот был добавлен. Это окно можно закрыть.',
  denied: 'Доступ не выдан, бот не подключён.',
  bad_scope: 'Не выданы нужные права: бот должен иметь право писать в чат. Попробуйте ещё раз.',
  bad_state: 'Ссылка устарела или уже использована. Создайте новую в панели стримера.',
  failed: 'Не удалось подключить бота. Попробуйте ещё раз.',
}
const text = computed(() => texts[status.value] ?? texts.failed)
</script>

<template>
  <section class="card center">
    <p class="result" :class="ok ? '' : 'err'" role="status">{{ text }}</p>
  </section>
</template>
