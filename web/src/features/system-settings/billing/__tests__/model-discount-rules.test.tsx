import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ModelDiscountRulesCard } from '../model-discount-rules-card'
import { addDiscountRule, parseDiscountRules, removeDiscountRule } from '../model-discount-rules'

vi.mock('@tanstack/react-router', () => ({ useBlocker: () => ({ status: 'idle' }) }))
afterEach(() => vi.restoreAllMocks())

function setup(value = '{}', success = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/option/') return { data: { success, data: [{ key: 'ModelDiscountRules', value }] } }
    if (url === '/api/group/') return { data: { success: true, data: ['vip'] } }
    if (url === '/api/option/model_discount_models') return { data: { success: true, data: ['exact-model', 'other-model', 'custom-channel-model'] } }
    return { data: { success: true, data: { items: [{ id: 123, username: 'customer' }], total: 1, page: 1, page_size: 20 } } }
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  render(<QueryClientProvider client={client}><ModelDiscountRulesCard /></QueryClientProvider>)
  return put
}

async function choose(label: string, option: string) {
  const input = screen.getByRole('combobox', { name: label })
  await userEvent.click(input)
  await userEvent.type(input, option)
  await userEvent.click(await screen.findByRole('option', { name: option }))
}

describe('model discount editor', () => {
  it('starts empty without default discounts and validates duplicates/zero/range', () => {
    expect(parseDiscountRules('{}')).toEqual({})
    const row = { kind: 'users' as const, owner: '123', model: 'exact-model', factor: 0 }
    const next = addDiscountRule({}, row)
    expect(next).toEqual({ users: { '123': { 'exact-model': 0 } } })
    expect(() => addDiscountRule(next, { ...row, factor: 1 })).toThrow()
    expect(() => addDiscountRule({}, { ...row, factor: 1.01 })).toThrow()
    expect(removeDiscountRule({ users: { '123': { 'exact-model': 0, other: 0.9 } } }, row)).toEqual({ users: { '123': { other: 0.9 } } })
  })

  it('adds explicitly, shows eight tenths, saves one complete option, and confirms row-only deletion', async () => {
    const put = setup()
    await screen.findByText(/暂无折扣规则/)
    expect(put).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByRole('combobox', { name: '折扣客户' })).toBeEnabled())
    await choose('折扣客户', 'vip')
    await choose('精确模型', 'exact-model')
    await userEvent.clear(screen.getByLabelText('实付比例（0–100%）'))
    await userEvent.type(screen.getByLabelText('实付比例（0–100%）'), '80')
    await userEvent.click(screen.getByRole('button', { name: '新增规则' }))
    expect(screen.getByText('80% · 8折')).toBeVisible()
    expect(screen.getByText('Unsaved changes')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: '保存全部规则' }))
    await waitFor(() => expect(put).toHaveBeenCalledWith('/api/option/', { key: 'ModelDiscountRules', value: JSON.stringify({ groups: { vip: { 'exact-model': 0.8 } } }), expected_value: '{}' }))
    await waitFor(() => expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/vip · exact-model/)).toBeVisible()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(screen.getByText(/暂无折扣规则/)).toBeVisible()
    expect(put).toHaveBeenCalledTimes(1)
    await userEvent.click(screen.getByRole('button', { name: '保存全部规则' }))
    await waitFor(() => expect(put).toHaveBeenLastCalledWith('/api/option/', { key: 'ModelDiscountRules', value: '{}', expected_value: JSON.stringify({ groups: { vip: { 'exact-model': 0.8 } } }) }))
  })

  it('offers channel-declared custom models and preserves a draft on version conflict', async () => {
    const put = setup()
    await screen.findByText(/暂无折扣规则/)
    await waitFor(() => expect(screen.getByRole('combobox', { name: '折扣客户' })).toBeEnabled())
    await choose('折扣客户', 'vip')
    await choose('精确模型', 'custom-channel-model')
    await userEvent.click(screen.getByRole('button', { name: '新增规则' }))
    put.mockResolvedValueOnce({ data: { success: false, message: '折扣规则已被其他管理员修改，请重新加载后合并修改' } })
    await userEvent.click(screen.getByRole('button', { name: '保存全部规则' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('其他管理员修改')
    expect(screen.getByText('custom-channel-model')).toBeVisible()
    expect(screen.getByText('Unsaved changes')).toBeVisible()
    expect(screen.getByRole('button', { name: '重新加载最新规则' })).toBeVisible()
    expect(put).toHaveBeenCalledWith('/api/option/', expect.objectContaining({ expected_value: '{}' }))
  })

  it('does not turn a failed load into an editable empty rule set', async () => {
    const put = setup('{}', false)
    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load settings')
    expect(screen.queryByRole('button', { name: '保存全部规则' })).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })
})
