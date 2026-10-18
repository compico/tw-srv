import { reactive } from 'vue'
import { api, ApiError, type Me } from './api'

type Phase = 'loading' | 'anon' | 'ready' | 'error'

// Общее состояние входа: им пользуются шапка и все страницы.
export const session = reactive({
  phase: 'loading' as Phase,
  me: null as Me | null,
  awaiting: 0, // заявок поддержки, ждущих ответа от этого пользователя
  isAdmin: false,
})

export async function loadSession() {
  try {
    session.me = await api.me()
    session.phase = 'ready'
    void refreshSupport()
  } catch (e) {
    session.phase = e instanceof ApiError && e.status === 401 ? 'anon' : 'error'
  }
}

export async function refreshSupport() {
  try {
    const s = await api.supportSummary()
    session.awaiting = s.awaiting
    session.isAdmin = s.isAdmin
  } catch {
    /* бейдж необязателен */
  }
}

export async function logout() {
  await api.logout().catch(() => {})
  session.me = null
  session.awaiting = 0
  session.isAdmin = false
  session.phase = 'anon'
}
