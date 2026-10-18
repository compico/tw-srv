<script setup lang="ts">
import { useId } from 'vue'
import { MdPreview } from 'md-editor-v3'
import { commonProps } from '../markdown/setup'

defineProps<{ body: string }>()
// У каждого просмотра свой id: на странице их несколько (по одному на сообщение).
const id = `mdv${useId()}`
const theme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
</script>

<template>
  <MdPreview v-bind="commonProps" :model-value="body" :theme="theme" :no-img-zoom-in="true" :editor-id="id" class="md-view" />
</template>
