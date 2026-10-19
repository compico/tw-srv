import { CSRF_HEADER, csrfToken } from './csrf'

export interface RewardForm {
  title: string
  prompt: string
  cost: number
  bonusAmount: number
  useUserInput: boolean
  backgroundColor: string
  enabled: boolean
  cooldownSeconds: number
  maxPerStream: number
  maxPerUserPerStream: number
}

export interface Reward extends RewardForm {
  twitchRewardId: string
  updatedAt: string
}

export interface Me {
  twitch: { id: string; login: string; displayName: string; avatarUrl: string }
  twitchReauth: boolean
  memealerts: { connected: boolean; needsReauth: boolean; name?: string; link?: string }
  reward: Reward | null
}

export interface Redemption {
  id: string
  viewerName: string
  userInput: string
  matchedNick: string
  amount: number
  status: 'received' | 'giving' | 'fulfilled' | 'refunded' | 'needs_review'
  reason: string
  createdAt: string
}

export type TicketStatus = 'open' | 'waiting' | 'closed'

export interface Ticket {
  id: number
  subject: string
  authorName: string
  mine: boolean
  status: TicketStatus
  createdAt: string
  updatedAt: string
}

export interface TicketMessage {
  id: number
  authorName: string
  mine: boolean
  staff: boolean
  format: string
  body: string
  createdAt: string
}

export interface TicketDetail {
  ticket: Ticket
  messages: TicketMessage[]
}

export interface SupportSummary {
  awaiting: number
  isAdmin: boolean
}

export interface UploadedImage {
  id: string
  url: string
  name: string
  contentType: string
  size: number
}

export const MAX_IMAGE_BYTES = 5 * 1024 * 1024

export interface PushStatus {
  enabled: boolean
  publicKey: string
  devices: number
}

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code?: string,
    public field?: string,
  ) {
    super(message)
  }
}

async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(data.error ?? `Ошибка ${res.status}`, res.status, data.code, data.field)
  return data as T
}

export const api = {
  me: () => request<Me>('GET', '/api/me'),
  saveReward: (r: RewardForm) => request<Reward>('PUT', '/api/reward', r),
  deleteReward: () => request<void>('DELETE', '/api/reward'),
  disconnectMemeAlerts: () => request<void>('DELETE', '/api/memealerts'),
  redemptions: () => request<Redemption[]>('GET', '/api/redemptions?limit=50'),
  supportSummary: () => request<SupportSummary>('GET', '/api/support/summary'),
  tickets: (scope: 'mine' | 'all') => request<Ticket[]>('GET', `/api/support/tickets?scope=${scope}`),
  createTicket: (subject: string, body: string) => request<Ticket>('POST', '/api/support/tickets', { subject, body }),
  ticket: (id: number) => request<TicketDetail>('GET', `/api/support/tickets/${id}`),
  reply: (id: number, body: string) => request<TicketDetail>('POST', `/api/support/tickets/${id}/messages`, { body }),
  closeTicket: (id: number) => request<TicketDetail>('POST', `/api/support/tickets/${id}/close`),
  logout: () => request<void>('POST', '/auth/logout'),
}

// Загрузка картинки для markdown-сообщения: multipart + CSRF-токен. Один повтор, если токен устарел.
export async function uploadImage(file: File): Promise<UploadedImage> {
  if (file.size > MAX_IMAGE_BYTES) throw new ApiError('Файл больше 5 МБ', 413, 'too_large')
  for (let attempt = 0; ; attempt++) {
    const body = new FormData()
    body.append('file', file)
    const res = await fetch('/api/uploads/images', {
      method: 'POST',
      headers: { Accept: 'application/json', [CSRF_HEADER]: await csrfToken(attempt > 0) },
      body,
    })
    const data = await res.json().catch(() => ({}))
    if (res.ok) return data as UploadedImage
    if (res.status === 403 && data.code === 'csrf' && attempt === 0) continue
    throw new ApiError(data.error ?? `Ошибка ${res.status}`, res.status, data.code)
  }
}

// POST с CSRF-токеном и одним повтором, если токен устарел.
async function postCsrf<T>(url: string, body?: unknown): Promise<T> {
  for (let attempt = 0; ; attempt++) {
    const res = await fetch(url, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', [CSRF_HEADER]: await csrfToken(attempt > 0) },
      body: JSON.stringify(body ?? {}),
    })
    if (res.status === 204) return undefined as T
    const data = await res.json().catch(() => ({}))
    if (res.ok) return data as T
    if (res.status === 403 && data.code === 'csrf' && attempt === 0) continue
    throw new ApiError(data.error ?? `Ошибка ${res.status}`, res.status, data.code)
  }
}

export const pushApi = {
  status: () => request<PushStatus>('GET', '/api/push/status'),
  subscribe: (sub: PushSubscriptionJSON) => postCsrf<void>('/api/push/subscribe', sub),
  unsubscribe: (endpoint: string) => postCsrf<void>('/api/push/unsubscribe', { endpoint }),
  test: () => postCsrf<{ sent: number }>('/api/push/test'),
}
