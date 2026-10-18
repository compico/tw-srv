<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, ApiError, type TicketDetail } from '../api'
import MessageBody from '../components/MessageBody.vue'
import StatusPill from '../components/StatusPill.vue'
import { refreshSupport } from '../session'

const props = defineProps<{ id: string }>()
const ticketId = computed(() => Number(props.id))

const data = ref<TicketDetail | null>(null)
const notFound = ref(false)
const error = ref('')
const reply = ref('')
const sending = ref(false)

const closed = computed(() => data.value?.ticket.status === 'closed')

async function load() {
  try {
    data.value = await api.ticket(ticketId.value)
    error.value = ''
    void refreshSupport()
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else error.value = e instanceof Error ? e.message : 'Не удалось загрузить заявку'
  }
}

async function send() {
  if (!reply.value.trim()) return
  error.value = ''
  sending.value = true
  try {
    data.value = await api.reply(ticketId.value, reply.value)
    reply.value = ''
    void refreshSupport()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Не удалось отправить сообщение'
    if (e instanceof ApiError && e.status === 409) await load()
  } finally {
    sending.value = false
  }
}

async function close() {
  if (!confirm('Закрыть заявку? После закрытия писать в неё нельзя.')) return
  try {
    data.value = await api.closeTicket(ticketId.value)
    void refreshSupport()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Не удалось закрыть заявку'
  }
}

const fmt = (iso: string) =>
  new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })

// Кто написал: свои сообщения помечаются «Вы», ответы администратора пользователю «Поддержка».
function who(m: TicketDetail['messages'][number]) {
  if (m.mine) return 'Вы'
  return m.staff ? 'Поддержка' : m.authorName
}

let timer: number | undefined
onMounted(() => {
  load()
  timer = window.setInterval(load, 15000)
})
onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <div>
    <p><RouterLink to="/support">← Все заявки</RouterLink></p>

    <section v-if="notFound" class="card center">
      <p>Заявка не найдена.</p>
    </section>

    <template v-else-if="data">
      <section class="card">
        <div class="head">
          <h2>{{ data.ticket.subject }}</h2>
          <StatusPill :status="data.ticket.status" />
        </div>
        <p class="muted small">
          #{{ data.ticket.id }} · {{ data.ticket.mine ? 'ваша заявка' : data.ticket.authorName }} · {{ fmt(data.ticket.createdAt) }}
        </p>
        <button v-if="!closed" class="btn danger" @click="close">Закрыть заявку</button>
      </section>

      <section class="chat" aria-label="Переписка">
        <article v-for="m in data.messages" :key="m.id" class="msg" :class="{ mine: m.mine, staff: m.staff }">
          <header>
            <b>{{ who(m) }}</b>
            <time class="muted">{{ fmt(m.createdAt) }}</time>
          </header>
          <MessageBody :body="m.body" :format="m.format" />
        </article>
      </section>

      <section class="card">
        <p v-if="closed" class="muted">Заявка закрыта. Если вопрос остался, создайте новую.</p>
        <form v-else @submit.prevent="send">
          <fieldset :disabled="sending">
            <label>Ответ
              <textarea v-model="reply" rows="4" maxlength="5000" required />
            </label>
            <p v-if="error" class="err" role="alert">{{ error }}</p>
            <button class="btn primary" type="submit">{{ sending ? 'Отправляю…' : 'Отправить' }}</button>
          </fieldset>
        </form>
        <p v-if="closed && error" class="err" role="alert">{{ error }}</p>
      </section>
    </template>

    <section v-else-if="error" class="card center">
      <p class="err">{{ error }}</p>
      <button class="btn" @click="load">Повторить</button>
    </section>
    <section v-else class="card muted">Загрузка…</section>
  </div>
</template>
