import { afterEach, expect, it, vi } from 'vitest'
import { refreshAccess } from './api'

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
