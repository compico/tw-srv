<script setup lang="ts">
import { defineAsyncComponent } from 'vue'

// Редактор подгружается отдельным куском: панель стримера его не тянет.
const MarkdownView = defineAsyncComponent(() => import('./MarkdownView.vue'))

// format: 'markdown' (новые сообщения) или 'text' (ранние: простой текст).
defineProps<{ body: string; format: string }>()
</script>

<template>
  <MarkdownView v-if="format === 'markdown'" :body="body" />
  <p v-else class="msg-body">{{ body }}</p>
</template>
