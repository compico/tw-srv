<script setup lang="ts">
// Текст сообщения. Сейчас формат только простой текст (интерполяция Vue экранирует HTML).
// Когда появится markdown, здесь добавится ветка для format === 'markdown'.
defineProps<{ body: string; format: string }>()
</script>

<template>
  <p class="msg-body">{{ body }}</p>
</template>
