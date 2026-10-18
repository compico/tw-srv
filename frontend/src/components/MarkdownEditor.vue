<script setup lang="ts">
import { computed } from 'vue'
import { MdEditor, type ToolbarNames } from 'md-editor-v3'
import { uploadImage } from '../api'
import { commonProps } from '../markdown/setup'

const props = defineProps<{ modelValue: string; maxLength?: number; placeholder?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; error: [message: string] }>()

const text = computed({
  get: () => props.modelValue,
  set: (v: string) => emit('update:modelValue', v),
})

const theme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'

// Без github, сохранения, форматирования, mermaid, katex и полноэкранного режима (лишние скрипты).
const toolbars: ToolbarNames[] = [
  'bold', 'underline', 'italic', 'strikeThrough', '-',
  'title', 'quote', 'unorderedList', 'orderedList', 'task', '-',
  'codeRow', 'code', 'link', 'image', 'table', '-',
  'revoke', 'next', '=', 'preview',
]

// Загрузка картинок: каждый файл уходит на сервер, в текст вставляется только ссылка на него.
async function onUploadImg(files: File[], callback: (urls: string[]) => void) {
  const urls: string[] = []
  for (const f of files) {
    try {
      urls.push((await uploadImage(f)).url)
    } catch (e) {
      emit('error', `${f.name}: ${e instanceof Error ? e.message : 'не удалось загрузить'}`)
    }
  }
  callback(urls)
}
</script>

<template>
  <MdEditor
    v-model="text"
    v-bind="commonProps"
    :theme="theme"
    :toolbars="toolbars"
    :footers="['markdownTotal', '=', 'scrollSwitch']"
    :max-length="maxLength"
    :placeholder="placeholder"
    :on-upload-img="onUploadImg"
    :preview="false"
    :auto-detect-code="false"
    editor-id="support-editor"
    class="md-field"
  />
</template>
