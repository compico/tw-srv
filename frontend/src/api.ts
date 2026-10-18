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
  logout: () => request<void>('POST', '/auth/logout'),
}
