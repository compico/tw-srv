<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, type Ticket } from '../api'
import PushToggle from '../components/PushToggle.vue'
import StatusPill from '../components/StatusPill.vue'
import { refreshSupport, session } from '../session'

const scope = ref<'mine' | 'all'>('mine')
const items = ref<Ticket[]>([])
const loaded = ref(false)
const error = ref('')
const isAdmin = computed(() => session.isAdmin)

async function load() {
  try {
    items.value = await api.tickets(scope.value)
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Не удалось загрузить заявки'
  } finally {
    loaded.value = true
  }
}

const fmt = (iso: string) =>
  new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })

let timer: number | undefined
onMounted(() => {
  load()
  refreshSupport()
  timer = window.setInterval(load, 30000)
})
onUnmounted(() => window.clearInterval(timer))
watch(scope, () => {
  loaded.value = false
  load()
})
</script>

<template>
  <div>
    <PushToggle v-if="isAdmin" />
    <section class="card">
      <div class="head">
        <h2>Поддержка</h2>
        <RouterLink class="btn primary" to="/support/new">Новая заявка</RouterLink>
      </div>

      <div v-if="isAdmin" class="tabs" role="tablist">
        <button class="tab" :class="{ on: scope === 'mine' }" role="tab" @click="scope = 'mine'">Мои заявки</button>
        <button class="tab" :class="{ on: scope === 'all' }" role="tab" @click="scope = 'all'">Все заявки</button>
      </div>

      <p v-if="error" class="err">{{ error }}</p>
      <p v-else-if="!loaded" class="muted">Загрузка…</p>
      <p v-else-if="!items.length" class="muted">
        {{ scope === 'all' ? 'Заявок пока нет.' : 'У вас пока нет заявок. Нажмите «Новая заявка», если что-то не работает или есть вопрос.' }}
      </p>
      <ul v-else class="tickets">
        <li v-for="t in items" :key="t.id">
          <RouterLink :to="`/support/${t.id}`" class="ticket">
            <span class="subject">{{ t.subject }}</span>
            <span class="meta">
              <span v-if="scope === 'all' && !t.mine">{{ t.authorName }} · </span>#{{ t.id }} · {{ fmt(t.updatedAt) }}
            </span>
            <StatusPill :status="t.status" />
          </RouterLink>
        </li>
      </ul>
    </section>
  </div>
</template>
