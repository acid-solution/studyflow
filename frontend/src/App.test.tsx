import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'

vi.mock('./api', async () => {
  const actual = await vi.importActual<typeof import('./api')>('./api')
  return {
    ...actual,
    refreshAccess: vi.fn().mockRejectedValue(new Error('no session')),
    requestVerification: vi.fn().mockResolvedValue({ challenge_id: 'challenge', expires_at: '', resend_after: '' }),
  }
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('StudyFlow authentication entry', () => {
  it('shows the login form when refresh cookie is unavailable', async () => {
    render(<App />)
    expect(await screen.findByPlaceholderText('name@example.com')).toBeInTheDocument()
    expect(screen.getByText('把长期目标变成今天可以完成的行动')).toBeInTheDocument()
  })

  it('requests a registration code before asking for the password', async () => {
    render(<App />)
    await screen.findByPlaceholderText('name@example.com')
    fireEvent.click(screen.getByRole('button', { name: '注册' }))
    fireEvent.change(screen.getByPlaceholderText('name@example.com'), { target: { value: 'person@example.com' } })
    fireEvent.click(screen.getByRole('button', { name: '发送验证码' }))
    await waitFor(() => expect(screen.getByPlaceholderText('000000')).toBeInTheDocument())
    expect(screen.getByPlaceholderText('至少 8 位')).toBeInTheDocument()
  })
})
