// CSRF-токен текущей сессии для заголовка X-CSRF-Token (изменяющие запросы к защищённым маршрутам).
let cached: string | null = null

export async function csrfToken(refresh = false): Promise<string> {
  if (cached && !refresh) return cached
  const res = await fetch('/api/csrf', { headers: { Accept: 'application/json' } })
  if (!res.ok) throw new Error('Не удалось получить токен защиты, обновите страницу')
  const data = (await res.json()) as { token: string }
  cached = data.token
  return cached
}

export const CSRF_HEADER = 'X-CSRF-Token'
