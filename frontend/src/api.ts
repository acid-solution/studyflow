export const AUTH_URL = import.meta.env.VITE_AUTH_URL ?? 'http://127.0.0.1:18082'
export const API_URL = import.meta.env.VITE_API_URL ?? 'http://127.0.0.1:8081'

export class APIError extends Error {
  status: number
  code?: string | number
  constructor(message: string, status: number, code?: string | number) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function json<T>(response: Response): Promise<T> {
  if (response.status === 204) return undefined as T
  const body = await response.json().catch(() => ({}))
  if (!response.ok) {
    const nested = body?.error
    throw new APIError(nested?.message ?? body?.message ?? '请求失败', response.status, nested?.code ?? body?.code)
  }
  return (body?.data ?? body) as T
}

export type Account = {
  user: { id: string; status: string }
  identities: Array<{ kind: string; value: string; verified_at: string }>
}

export type TokenIssue = {
  account: Account
  access_token: string
  expires_at: string
}

export async function login(email: string, password: string) {
  return json<TokenIssue>(await fetch(`${AUTH_URL}/auth/v1/login`, {
    method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ client_id: 'studyflow', email, password }),
  }))
}

export async function refreshAccess() {
  return json<TokenIssue>(await fetch(`${AUTH_URL}/auth/v1/token/refresh`, {
    method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ client_id: 'studyflow' }),
  }))
}

export async function logout() {
  await fetch(`${AUTH_URL}/auth/v1/logout`, {
    method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ client_id: 'studyflow' }),
  })
}

export async function requestVerification(email: string) {
  return json<{ challenge_id: string; expires_at: string; resend_after: string }>(await fetch(`${AUTH_URL}/auth/v1/verifications`, {
    method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ client_id: 'studyflow', email, purpose: 'register' }),
  }))
}

export async function register(challengeID: string, code: string, password: string) {
  return json<TokenIssue>(await fetch(`${AUTH_URL}/auth/v1/register`, {
    method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ client_id: 'studyflow', challenge_id: challengeID, code, password }),
  }))
}

export async function api<T>(token: string, path: string, options: RequestInit = {}) {
  const headers = new Headers(options.headers)
  headers.set('Authorization', `Bearer ${token}`)
  if (options.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  return json<T>(await fetch(`${API_URL}/api/v1${path}`, { ...options, headers }))
}
