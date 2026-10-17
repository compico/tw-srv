<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { api, ApiError, type Me, type Redemption, type RewardForm } from './api'

type Phase = 'loading' | 'anon' | 'ready' | 'error'

const phase = ref<Phase>('loading')
const me = ref<Me | null>(null)
const items = ref<Redemption[]>([])
const flash = ref<{ kind: 'ok' | 'err'; text: string } | null>(null)
const saving = ref(false)
const fieldError = ref('')

const form = reactive<RewardForm>(defaults())
// Лимиты в форме: чекбокс + число, на сервер уходит 0 для «выключено».
const cooldownOn = ref(false)
const cooldownMinutes = ref(5)
const perStreamOn = ref(false)
const perUserOnce = ref(true)

function defaults(): RewardForm {
  return {
    title: 'Получить мемкоины',
    prompt: 'Введите свой ник на MemeAlerts. Баллы вернутся, если ника нет среди саппортеров.',
    cost: 1000,
    bonusAmount: 10,
    useUserInput: true,
    backgroundColor: '#9146FF',
    enabled: true,
    cooldownSeconds: 0,
    maxPerStream: 0,
    maxPerUserPerStream: 1,
  }
}

function fillForm() {
  const r = me.value?.reward
  Object.assign(form, r ? { ...r } : defaults())
  cooldownOn.value = form.cooldownSeconds > 0
  cooldownMinutes.value = form.cooldownSeconds > 0 ? Math.max(1, Math.round(form.cooldownSeconds / 60)) : 5
  perStreamOn.value = form.maxPerStream > 0
  if (!perStreamOn.value) form.maxPerStream = 100
  perUserOnce.value = form.maxPerUserPerStream > 0
}

function say(kind: 'ok' | 'err', text: string) {
  flash.value = { kind, text }
  window.setTimeout(() => (flash.value = null), 6000)
}

async function load() {
  try {
    me.value = await api.me()
    phase.value = 'ready'
    fillForm()
    await loadHistory()
  } catch (e) {
    phase.value = e instanceof ApiError && e.status === 401 ? 'anon' : 'error'
  }
}

async function loadHistory() {
  try {
    items.value = await api.redemptions()
  } catch {
    /* история необязательна */
  }
}

async function save() {
  fieldError.value = ''
  saving.value = true
  try {
    const payload: RewardForm = {
      ...form,
      cooldownSeconds: cooldownOn.value ? Math.round(cooldownMinutes.value * 60) : 0,
      maxPerStream: perStreamOn.value ? form.maxPerStream : 0,
      maxPerUserPerStream: perUserOnce.value ? 1 : 0,
    }
    await api.saveReward(payload)
    say('ok', 'Награда сохранена на вашем канале Twitch')
    await load()
  } catch (e) {
    if (e instanceof ApiError) {
      fieldError.value = e.field ?? ''
      say('err', e.message)
    } else say('err', 'Не удалось сохранить')
  } finally {
    saving.value = false
  }
}

async function removeReward() {
  if (!confirm('Удалить награду с канала Twitch?')) return
  try {
    await api.deleteReward()
    say('ok', 'Награда удалена')
    await load()
  } catch (e) {
    say('err', e instanceof Error ? e.message : 'Не удалось удалить')
  }
}

async function disconnect() {
  if (!confirm('Отключить MemeAlerts? Награда будет выключена.')) return
  try {
    await api.disconnectMemeAlerts()
    await load()
  } catch (e) {
    say('err', e instanceof Error ? e.message : 'Не удалось отключить')
  }
}

async function logout() {
  await api.logout().catch(() => {})
  me.value = null
  phase.value = 'anon'
}

const connected = computed(() => !!me.value?.memealerts.connected && !me.value.memealerts.needsReauth)

const statusText: Record<Redemption['status'], string> = {
  received: 'В обработке',
  giving: 'Выдаётся',
  fulfilled: 'Выдано',
  refunded: 'Баллы возвращены',
  needs_review: 'Нужна проверка',
}

const fmt = (iso: string) =>
  new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })

let timer: number | undefined
onMounted(() => {
  // Сообщения после возврата с MemeAlerts: /?memealerts=connected | /?memealerts_error=...
  const q = new URLSearchParams(location.search)
  if (q.get('memealerts') === 'connected') say('ok', 'MemeAlerts подключён')
  else if (q.get('memealerts_error')) say('err', 'Не удалось подключить MemeAlerts, попробуйте ещё раз')
  else if (q.get('login_error')) say('err', 'Вход через Twitch отменён')
  if (q.toString()) history.replaceState(null, '', '/')

  load()
  timer = window.setInterval(() => phase.value === 'ready' && loadHistory(), 15000)
})
onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <main class="wrap">
    <header class="top">
      <h1>Мемкоины за баллы канала</h1>
      <div v-if="me" class="who">
        <img v-if="me.twitch.avatarUrl" :src="me.twitch.avatarUrl" alt="" />
        <span>{{ me.twitch.displayName }}</span>
        <button class="link" @click="logout">Выйти</button>
      </div>
    </header>

    <p v-if="flash" class="flash" :class="flash.kind" role="status">{{ flash.text }}</p>

    <section v-if="phase === 'loading'" class="card muted">Загрузка…</section>

    <section v-else-if="phase === 'anon'" class="card center">
      <p>Зрители обменивают баллы канала на мемкоины прямо на вашем стриме. Войдите через Twitch, чтобы настроить награду.</p>
      <a class="btn primary" href="/login">Войти через Twitch</a>
    </section>

    <section v-else-if="phase === 'error'" class="card center">
      <p>Не удалось загрузить данные.</p>
      <button class="btn" @click="load">Повторить</button>
    </section>

    <template v-else-if="me">
      <section v-if="me.twitchReauth" class="card warn">
        <p>Twitch отозвал доступ к каналу, приём заявок остановлен.</p>
        <a class="btn primary" href="/login">Войти заново</a>
      </section>

      <section class="card">
        <h2><span class="n">1</span> Аккаунт MemeAlerts</h2>
        <template v-if="connected">
          <p>
            Подключён: <b>{{ me.memealerts.name }}</b>
            <a v-if="me.memealerts.link" :href="`https://memealerts.com/${me.memealerts.link}`" target="_blank" rel="noopener">
              memealerts.com/{{ me.memealerts.link }}
            </a>
          </p>
          <button class="btn" @click="disconnect">Отключить</button>
        </template>
        <template v-else>
          <p v-if="me.memealerts.needsReauth" class="err">Доступ к MemeAlerts отозван. Подключите аккаунт заново.</p>
          <p v-else class="muted">Нужен, чтобы проверять саппортеров и начислять мемкоины от вашего имени.</p>
          <a class="btn primary" href="/api/memealerts/connect">Подключить MemeAlerts</a>
        </template>
      </section>

      <section class="card" :class="{ disabled: !connected }">
        <h2><span class="n">2</span> Награда на Twitch</h2>
        <form @submit.prevent="save">
          <fieldset :disabled="!connected || saving">
            <label>Название
              <input v-model="form.title" maxlength="45" required :class="{ bad: fieldError === 'title' }" />
            </label>
            <label>Описание для зрителей
              <textarea v-model="form.prompt" maxlength="200" rows="2" />
            </label>

            <div class="row">
              <label>Цена, баллов
                <input v-model.number="form.cost" type="number" min="1" required />
              </label>
              <label>Выдаётся мемкоинов
                <input v-model.number="form.bonusAmount" type="number" min="1" max="1000" required />
              </label>
              <label class="color">Цвет
                <input v-model="form.backgroundColor" type="color" />
              </label>
            </div>

            <label class="check">
              <input v-model="form.useUserInput" type="checkbox" />
              Зритель вводит ник MemeAlerts
              <small>Если выключено, используется его ник на Twitch.</small>
            </label>

            <h3>Как часто можно использовать</h3>
            <label class="check">
              <input v-model="perUserOnce" type="checkbox" />
              Один раз за стрим на зрителя
            </label>
            <label class="check">
              <input v-model="cooldownOn" type="checkbox" />
              Общая перезарядка
              <span v-if="cooldownOn" class="inline">
                <input v-model.number="cooldownMinutes" type="number" min="1" max="10080" /> мин
              </span>
              <small>Пауза между использованиями награды любым зрителем.</small>
            </label>
            <label class="check">
              <input v-model="perStreamOn" type="checkbox" />
              Максимум за стрим
              <span v-if="perStreamOn" class="inline"><input v-model.number="form.maxPerStream" type="number" min="1" /> раз всего</span>
            </label>

            <label class="check">
              <input v-model="form.enabled" type="checkbox" />
              Награда включена
            </label>

            <div class="actions">
              <button class="btn primary" type="submit">{{ saving ? 'Сохраняю…' : me.reward ? 'Сохранить' : 'Создать награду' }}</button>
              <button v-if="me.reward" class="btn danger" type="button" @click="removeReward">Удалить награду</button>
            </div>
          </fieldset>
        </form>
        <p class="note">
          Если ника нет среди ваших саппортеров, баллы автоматически возвращаются зрителю.
          Лимиты за стрим считает сам Twitch.
        </p>
      </section>

      <section class="card">
        <h2>Последние использования</h2>
        <p v-if="!items.length" class="muted">Пока никто не получал мемкоины.</p>
        <table v-else>
          <thead>
            <tr><th>Когда</th><th>Зритель</th><th>Ник</th><th>Мемкоины</th><th>Итог</th></tr>
          </thead>
          <tbody>
            <tr v-for="h in items" :key="h.id">
              <td>{{ fmt(h.createdAt) }}</td>
              <td>{{ h.viewerName }}</td>
              <td>{{ h.matchedNick || h.userInput || '—' }}</td>
              <td>{{ h.status === 'fulfilled' ? h.amount : '—' }}</td>
              <td>
                <span class="pill" :class="h.status">{{ statusText[h.status] }}</span>
                <small v-if="h.reason" class="reason">{{ h.reason }}</small>
              </td>
            </tr>
          </tbody>
        </table>
      </section>
    </template>
  </main>
</template>
