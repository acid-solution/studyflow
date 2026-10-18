import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from './App'
import { api, deleteAccount, refreshAccess, type TokenIssue } from './api'
import { clearSyncCursor } from './sync'

let renew: () => Promise<void>
vi.mock('./api', async () => ({ ...await vi.importActual('./api'), api: vi.fn(), deleteAccount: vi.fn(), refreshAccess: vi.fn() }))
vi.mock('./sync', async () => ({
  ...await vi.importActual('./sync'),
  SyncProvider: ({ children, onUnauthorized }: { children: ReactNode; onUnauthorized: () => Promise<void> }) => { renew = onUnauthorized; return children },
  SyncStatus: () => null,
  clearSyncCursor: vi.fn(),
}))

const issue: TokenIssue = { access_token: 'token', expires_at: '', account: { user: { id: 'user-id', status: 'active' }, identities: [{ kind: 'email', value: 'person@example.com', verified_at: '' }] } }
beforeEach(() => {
  vi.mocked(refreshAccess).mockResolvedValue(issue)
  vi.mocked(deleteAccount).mockResolvedValue(undefined)
  vi.mocked(api).mockImplementation(async (_token, path) => {
    if (path === '/dashboard/today') return { date: '2026-10-18', overdue: [], today: [], completed_today: [], sequence_plans: [], future: [], running_session: null } as never
    if (path === '/preferences') return { week_start: 'monday', default_plan_mode: 'calendar', show_completed_today: true } as never
    return [] as never
  })
})
afterEach(() => { cleanup(); vi.resetAllMocks() })

async function openConfirmation() {
  render(<App />)
  fireEvent.click(await screen.findByRole('button', { name: '设置' }))
  fireEvent.click(await screen.findByRole('button', { name: '注销账号' }))
  fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'password' } })
  fireEvent.click(screen.getByRole('checkbox'))
}

it('returns to login and clears the sync cursor only after successful deletion', async () => {
  await openConfirmation()
  fireEvent.click(screen.getByRole('button', { name: '永久注销账号' }))
  expect(await screen.findByPlaceholderText('name@example.com')).toBeInTheDocument()
  expect(deleteAccount).toHaveBeenCalledExactlyOnceWith('token', 'password')
  expect(clearSyncCursor).toHaveBeenCalledExactlyOnceWith('user-id')
})

it('keeps the user signed in and preserves the sync cursor on failure', async () => {
  vi.mocked(deleteAccount).mockRejectedValue(new Error('当前密码错误'))
  await openConfirmation()
  fireEvent.click(screen.getByRole('button', { name: '永久注销账号' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('当前密码错误')
  expect(screen.queryByPlaceholderText('name@example.com')).not.toBeInTheDocument()
  expect(clearSyncCursor).not.toHaveBeenCalled()
})

it('ignores an earlier renewal response after the account is deleted', async () => {
  await openConfirmation()
  let finish!: (value: TokenIssue) => void
  vi.mocked(refreshAccess).mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
  const refreshing = renew()
  fireEvent.click(screen.getByRole('button', { name: '永久注销账号' }))
  await screen.findByPlaceholderText('name@example.com')
  await act(async () => { finish(issue); await refreshing })
  await waitFor(() => expect(screen.getByPlaceholderText('name@example.com')).toBeInTheDocument())
})
