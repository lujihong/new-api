import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { ModelDiscountRulesCard } from '../model-discount-rules-card'
import { addDiscountRule, parseDiscountRules, parseDiscountPercent, removeDiscountRule } from '../model-discount-rules'

vi.mock('@tanstack/react-router', () => ({ useBlocker: () => ({ status: 'idle' }) }))
afterEach(() => { vi.restoreAllMocks(); useAuthStore.getState().auth.reset() })
let lastClient: QueryClient

function setup(value = '{}', success = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  lastClient = client
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

  it('rejects exponent underflow and preserves ordinary percentage precision', () => {
    for (const input of ['', '1e-999', '-1', '101', '0.0000001', 'Infinity']) expect(parseDiscountPercent(input)).toBeNull()
    expect(parseDiscountPercent('0')).toBe(0)
    expect(parseDiscountPercent('100')).toBe(1)
    expect(parseDiscountPercent('95')).toBe(0.95)
    expect(parseDiscountPercent('80.125')).toBe(0.80125)
  })

  it('edits only the selected ratio, cancels safely and explicitly confirms free pricing', async () => {
    const put = setup(JSON.stringify({ groups: { vip: { 'exact-model': 0.8, 'other-model': 0.9 } } }))
    await screen.findByText('80% · 8折')
    await userEvent.click(screen.getAllByRole('button', { name: '编辑' })[0])
    expect(screen.getByRole('combobox', { name: '折扣客户' })).toBeDisabled()
    await userEvent.clear(screen.getByLabelText('实付比例（0–100%）'))
    await userEvent.type(screen.getByLabelText('实付比例（0–100%）'), '60')
    await userEvent.click(screen.getByRole('button', { name: '取消编辑' }))
    expect(screen.getByText('80% · 8折')).toBeVisible()
    expect(put).not.toHaveBeenCalled()
    await userEvent.click(screen.getAllByRole('button', { name: '编辑' })[0])
    await userEvent.click(screen.getByRole('button', { name: '免费 (0折)' }))
    await userEvent.click(screen.getByRole('button', { name: '更新草稿规则' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/exact-model 将设为 0%/)).toBeVisible()
    expect(screen.getByText('80% · 8折')).toBeVisible()
    await userEvent.click(within(dialog).getByRole('button', { name: '确认免费' }))
    expect(screen.getByText('0% · 0折')).toBeVisible()
    expect(screen.getByText('90% · 9折')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: '保存全部规则' }))
    await waitFor(() => expect(put).toHaveBeenCalledWith('/api/option/', expect.objectContaining({ value: JSON.stringify({ groups: { vip: { 'exact-model': 0, 'other-model': 0.9 } } }) })))
  })

  it('bounds rule DOM to a page and leaves filtering usable with no matches', async () => {
    const models = Object.fromEntries(Array.from({ length: 10000 }, (_, i) => [`model-${String(i).padStart(5, '0')}`, 0.8]))
    setup(JSON.stringify({ groups: { vip: models } }))
    await screen.findByText('model-00000')
    expect(screen.getAllByRole('row')).toHaveLength(26)
    await userEvent.click(screen.getByRole('button', { name: '规则下一页' }))
    expect(screen.getByText('model-00025')).toBeVisible()
    await userEvent.type(screen.getByLabelText('筛选规则或模型'), 'not-existing')
    expect(screen.getByText('没有匹配的规则，请修改筛选条件。')).toBeVisible()
    await userEvent.clear(screen.getByLabelText('筛选规则或模型'))
    expect(screen.getByText('model-00000')).toBeVisible()
    expect(screen.getAllByRole('row')).toHaveLength(26)
  })

  it('locks edits and refresh while a saved snapshot is in flight', async () => {
    const put = setup(JSON.stringify({ groups: { vip: { 'exact-model': 0.8 } } }))
    await screen.findByText('80% · 8折')
    await userEvent.click(screen.getByRole('button', { name: '编辑' }))
    await userEvent.click(screen.getByRole('button', { name: '9.5折' }))
    await userEvent.click(screen.getByRole('button', { name: '更新草稿规则' }))
    let finish!: (value: { data: { success: boolean } }) => void
    put.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve }))
    await userEvent.click(screen.getByRole('button', { name: '保存全部规则' }))
    expect(screen.getByRole('button', { name: '编辑' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '刷新规则' })).toBeDisabled()
    expect(screen.getByRole('combobox', { name: '规则类型' })).toBeDisabled()
    finish({ data: { success: true } })
    await waitFor(() => expect(screen.getByRole('button', { name: '编辑' })).toBeEnabled())
    expect(screen.getByText('95% · 9.5折')).toBeVisible()
  })

  it('preserves edited draft and form input when a background refetch fails', async () => {
    setup(JSON.stringify({ groups: { vip: { 'exact-model': 0.8 } } }))
    await screen.findByText('80% · 8折')
    await userEvent.click(screen.getByRole('button', { name: '编辑' }))
    await userEvent.click(screen.getByRole('button', { name: '8.5折' }))
    await userEvent.click(screen.getByRole('button', { name: '更新草稿规则' }))
    await userEvent.type(screen.getByLabelText('筛选规则或模型'), 'exact')
    vi.mocked(api.get).mockRejectedValueOnce(new Error('offline'))
    await act(async () => { await lastClient.refetchQueries({ queryKey: ['model-discount-rules'], exact: false }) })
    expect(await screen.findByText(/当前草稿和未加入的输入已保留/)).toBeVisible()
    expect(screen.getByText('85% · 8.5折')).toBeVisible()
    expect(screen.getByText('Unsaved changes')).toBeVisible()
    expect(screen.getByLabelText('筛选规则或模型')).toHaveValue('exact')
  })

  it('does not refill the new session from a late old-session save', async () => {
    const session = (sid: string) => ({ sid, current: true, login_method: 'password', ip: '', user_agent: '', created_at: 0, last_active_at: 0, expires_at: 0 })
    useAuthStore.setState((state) => ({ auth: { ...state.auth, user: { id: 1, username: 'root', role: 100 }, session: session('first') } }))
    const baseline = JSON.stringify({ groups: { vip: { 'exact-model': 0.8 } } })
    const put = setup(baseline)
    await screen.findByText('80% · 8折')
    await userEvent.click(screen.getByRole('button', { name: '编辑' }))
    await userEvent.click(screen.getByRole('button', { name: '9.5折' }))
    await userEvent.click(screen.getByRole('button', { name: '更新草稿规则' }))
    let finish!: (value: { data: { success: boolean } }) => void
    put.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve }))
    await userEvent.click(screen.getByRole('button', { name: '保存全部规则' }))
    await act(async () => { useAuthStore.setState((state) => ({ auth: { ...state.auth, session: session('second') } })) })
    await screen.findByText('80% · 8折')
    await act(async () => { finish({ data: { success: true } }) })
    expect(screen.queryByText('95% · 9.5折')).not.toBeInTheDocument()
    expect(lastClient.getQueryData(['model-discount-rules', 1, 'second'])).toEqual(JSON.parse(baseline))
  })

  it('does not turn a failed load into an editable empty rule set', async () => {
    const put = setup('{}', false)
    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load settings')
    expect(screen.queryByRole('button', { name: '保存全部规则' })).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })
})
