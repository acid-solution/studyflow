import { afterEach, expect, it, vi } from 'vitest'
import { AUTH_URL, deleteAccount, refreshAccess } from './api'

afterEach(() => { vi.unstubAllGlobals() })
it('shares refresh-token rotation between concurrent refresh requests', async () => {
  let finish!: (value: Response) => void
  const fetch = vi.fn(() => new Promise<Response>((resolve) => { finish = resolve }))
  vi.stubGlobal('fetch', fetch)
  const one = refreshAccess(); const two = refreshAccess()
  expect(one).toBe(two); expect(fetch).toHaveBeenCalledTimes(1)
  finish(new Response(JSON.stringify({ access_token: 'new', account: {}, expires_at: '' }), { headers: { 'Content-Type': 'application/json' } }))
  expect((await one).access_token).toBe('new'); await two
})

it('uses the browser lock to coordinate refreshes across tabs', async () => {
  const original = Object.getOwnPropertyDescriptor(navigator, 'locks')
  const request = vi.fn(async (_name: string, callback: () => Promise<unknown>) => callback())
  Object.defineProperty(navigator, 'locks', { configurable: true, value: { request } })
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{"access_token":"locked"}', { headers: { 'Content-Type': 'application/json' } })))
  try { expect((await refreshAccess()).access_token).toBe('locked'); expect(request).toHaveBeenCalledWith('studyflow-auth-refresh', expect.any(Function)) }
  finally { if (original) Object.defineProperty(navigator, 'locks', original); else Reflect.deleteProperty(navigator, 'locks') }
})

it('allows retry after a failed refresh instead of keeping a rejected promise', async () => {
  const fetch = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(new Response('{"access_token":"retry"}', { headers: { 'Content-Type': 'application/json' } }))
  vi.stubGlobal('fetch', fetch)
  await expect(refreshAccess()).rejects.toThrow('offline')
  expect((await refreshAccess()).access_token).toBe('retry'); expect(fetch).toHaveBeenCalledTimes(2)
})

it('deletes the shared account with the access token and exact current password', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetch)
  await deleteAccount('access-token', ' password with spaces ')
  expect(fetch).toHaveBeenCalledExactlyOnceWith(`${AUTH_URL}/auth/v1/me`, {
    method: 'DELETE', credentials: 'include',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer access-token' },
    body: JSON.stringify({ current_password: ' password with spaces ' }),
  })
})

it('does not refresh or retry deletion on an incorrect password', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{"error":{"code":"invalid_password","message":"当前密码错误"}}', { status: 401 }))
  vi.stubGlobal('fetch', fetch)
  await expect(deleteAccount('access-token', 'wrong')).rejects.toMatchObject({ status: 401, code: 'invalid_password', message: '当前密码错误' })
  expect(fetch).toHaveBeenCalledTimes(1)
})

it('refreshes an expired access token once before retrying deletion', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response('{"error":{"code":"unauthorized"}}', { status: 401 }))
    .mockResolvedValueOnce(new Response('{"data":{"access_token":"fresh-token"}}'))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetch)
  await deleteAccount('expired-token', 'password')
  expect(fetch).toHaveBeenCalledTimes(3)
  expect(fetch.mock.calls[1][0]).toBe(`${AUTH_URL}/auth/v1/token/refresh`)
  expect(fetch.mock.calls[2][1].headers.Authorization).toBe('Bearer fresh-token')
})

it('does not retry deletion when the server fails', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{"error":{"code":"internal_error","message":"暂时不可用"}}', { status: 500 }))
  vi.stubGlobal('fetch', fetch)
  await expect(deleteAccount('access-token', 'password')).rejects.toMatchObject({ status: 500 })
  expect(fetch).toHaveBeenCalledTimes(1)
})
