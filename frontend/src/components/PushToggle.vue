<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { pushApi, type PushStatus } from '../api'

// Браузерные уведомления для администратора: Push API + service worker (/sw.js).
const supported = 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window

const status = ref<PushStatus | null>(null)
const subscribed = ref(false) // подписан именно этот браузер
const permission = ref<NotificationPermission>(supported ? Notification.permission : 'denied')
const busy = ref(false)
const message = ref('')
const failed = ref(false)

const canEnable = computed(() => supported && status.value?.enabled && permission.value !== 'denied')

function say(text: string, error = false) {
  message.value = text
  failed.value = error
}

// applicationServerKey приходит в base64url, браузеру нужны байты.
function keyBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const pad = '='.repeat((4 - (b64url.length % 4)) % 4)
  const raw = atob((b64url + pad).replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

async function registration() {
  return navigator.serviceWorker.register('/sw.js', { scope: '/' }).then(() => navigator.serviceWorker.ready)
}

async function refresh() {
  try {
    status.value = await pushApi.status()
    if (supported) {
      const reg = await navigator.serviceWorker.getRegistration('/')
      subscribed.value = !!(await reg?.pushManager.getSubscription())
    }
  } catch (e) {
    say(e instanceof Error ? e.message : 'Не удалось получить состояние уведомлений', true)
  }
}

async function enable() {
  if (!status.value) return
  busy.value = true
  say('')
  try {
    permission.value = await Notification.requestPermission()
    if (permission.value !== 'granted') {
      say('Браузер не разрешил уведомления. Разрешите их для этого сайта в настройках браузера.', true)
      return
    }
    const reg = await registration()
    const sub =
      (await reg.pushManager.getSubscription()) ??
      (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(status.value.publicKey) }))
    await pushApi.subscribe(sub.toJSON())
    say('Уведомления включены на этом устройстве')
    await refresh()
  } catch (e) {
    say(e instanceof Error ? e.message : 'Не удалось включить уведомления', true)
  } finally {
    busy.value = false
  }
}

async function disable() {
  busy.value = true
  say('')
  try {
    const reg = await navigator.serviceWorker.getRegistration('/')
    const sub = await reg?.pushManager.getSubscription()
    if (sub) {
      await pushApi.unsubscribe(sub.endpoint)
      await sub.unsubscribe()
    }
    say('Уведомления выключены на этом устройстве')
    await refresh()
  } catch (e) {
    say(e instanceof Error ? e.message : 'Не удалось выключить уведомления', true)
  } finally {
    busy.value = false
  }
}

async function test() {
  busy.value = true
  say('')
  try {
    const { sent } = await pushApi.test()
    say(sent > 0 ? 'Тестовое уведомление отправлено' : 'Сервер не смог доставить уведомление ни на одно устройство', sent === 0)
  } catch (e) {
    say(e instanceof Error ? e.message : 'Не удалось отправить', true)
  } finally {
    busy.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <section class="card push">
    <h2>Уведомления о заявках</h2>
    <p v-if="!supported" class="muted">
      Этот браузер не поддерживает push-уведомления. На iPhone и iPad они работают только у сайта, добавленного на экран «Домой».
    </p>
    <p v-else-if="status && !status.enabled" class="muted">
      Не настроено на сервере: задайте <code>VAPID_PUBLIC_KEY</code> и <code>VAPID_PRIVATE_KEY</code>
      (ключи: <code>docker compose run --rm memealerts vapid-keys</code>).
    </p>
    <template v-else-if="status">
      <p class="muted">
        Браузер покажет уведомление о новой заявке и о новом ответе пользователя, даже когда сайт закрыт.
        Устройств с подпиской: {{ status.devices }}.
      </p>
      <p v-if="permission === 'denied'" class="err">Уведомления для этого сайта заблокированы в настройках браузера.</p>
      <div class="actions">
        <button v-if="!subscribed" class="btn primary" :disabled="busy || !canEnable" @click="enable">Включить на этом устройстве</button>
        <template v-else>
          <button class="btn" :disabled="busy" @click="test">Отправить тест</button>
          <button class="btn danger" :disabled="busy" @click="disable">Выключить на этом устройстве</button>
        </template>
      </div>
    </template>
    <p v-if="message" class="note" :class="{ err: failed }" role="status">{{ message }}</p>
  </section>
</template>
