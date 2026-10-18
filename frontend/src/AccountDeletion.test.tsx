import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { AccountDeletion } from './AccountDeletion'

afterEach(cleanup)

function confirm() {
  fireEvent.click(screen.getByRole('button', { name: '注销账号' }))
  fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'current-password' } })
  fireEvent.click(screen.getByRole('checkbox'))
}

it('requires a password and explicit consent, and clears them on cancel', () => {
  const onDelete = vi.fn()
  render(<AccountDeletion onDelete={onDelete} />)
  fireEvent.click(screen.getByRole('button', { name: '注销账号' }))
  expect(screen.getByRole('button', { name: '永久注销账号' })).toBeDisabled()
  fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'password' } })
  expect(screen.getByRole('button', { name: '永久注销账号' })).toBeDisabled()
  fireEvent.click(screen.getByRole('checkbox'))
  expect(screen.getByRole('button', { name: '永久注销账号' })).toBeEnabled()
  fireEvent.click(screen.getByRole('button', { name: '取消' }))
  fireEvent.click(screen.getByRole('button', { name: '注销账号' }))
  expect(screen.getByLabelText('当前密码')).toHaveValue('')
  expect(screen.getByRole('checkbox')).not.toBeChecked()
  expect(onDelete).not.toHaveBeenCalled()
})

it('keeps the form usable after a wrong password', async () => {
  const onDelete = vi.fn().mockRejectedValue(new Error('当前密码错误'))
  render(<AccountDeletion onDelete={onDelete} />)
  confirm()
  fireEvent.click(screen.getByRole('button', { name: '永久注销账号' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('当前密码错误')
  expect(screen.getByLabelText('当前密码')).toBeEnabled()
  expect(screen.getByRole('button', { name: '永久注销账号' })).toBeEnabled()
})

it('sends one deletion while pending and disables all confirmation controls', async () => {
  let finish!: () => void
  const onDelete = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
  render(<AccountDeletion onDelete={onDelete} />)
  confirm()
  const form = screen.getByRole('form', { name: '确认注销共享账号' })
  fireEvent.submit(form)
  fireEvent.submit(form)
  expect(onDelete).toHaveBeenCalledExactlyOnceWith('current-password')
  expect(screen.getByLabelText('当前密码')).toBeDisabled()
  expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
  finish()
  await waitFor(() => expect(screen.getByLabelText('当前密码')).toHaveValue(''))
})
