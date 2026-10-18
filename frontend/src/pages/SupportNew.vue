<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, ApiError } from '../api'
import { refreshSupport } from '../session'

const router = useRouter()
const subject = ref('')
const body = ref('')
const sending = ref(false)
const error = ref('')
const field = ref('')

async function submit() {
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
          <label>Описание
            <textarea v-model="body" rows="8" maxlength="5000" required :class="{ bad: field === 'body' }" />
          </label>
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
