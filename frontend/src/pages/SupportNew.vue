<script setup lang="ts">
import { defineAsyncComponent, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, ApiError } from '../api'
import { refreshSupport } from '../session'

// Редактор тяжёлый, поэтому грузится отдельным куском и только на этой странице.
const MarkdownEditor = defineAsyncComponent(() => import('../components/MarkdownEditor.vue'))

const router = useRouter()
const subject = ref('')
const body = ref('')
const sending = ref(false)
const error = ref('')
const field = ref('')

async function submit() {
  if (!body.value.trim()) {
    field.value = 'body'
    error.value = 'Введите описание'
    return
  }
  error.value = ''
  field.value = ''
  sending.value = true
  try {
    const t = await api.createTicket(subject.value, body.value)
    void refreshSupport()
    await router.push(`/support/${t.id}`)
  } catch (e) {
    if (e instanceof ApiError) field.value = e.field ?? ''
    error.value = e instanceof Error ? e.message : 'Не удалось отправить заявку'
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div>
    <section class="card">
      <h2>Новая заявка</h2>
      <form @submit.prevent="submit">
        <fieldset :disabled="sending">
          <label>Тема
            <input v-model="subject" maxlength="120" required :class="{ bad: field === 'subject' }" />
          </label>
          <div class="field" :class="{ bad: field === 'body' }">
            <span class="label">Описание</span>
            <MarkdownEditor v-model="body" :max-length="5000" @error="error = $event" />
            <small class="muted">Поддерживается markdown. Картинку можно вставить из буфера или перетащить в поле, до 5 МБ.</small>
          </div>
          <p v-if="error" class="err" role="alert">{{ error }}</p>
          <div class="actions">
            <button class="btn primary" type="submit">{{ sending ? 'Отправляю…' : 'Отправить' }}</button>
            <RouterLink class="btn" to="/support">Отмена</RouterLink>
          </div>
        </fieldset>
      </form>
    </section>
  </div>
</template>
