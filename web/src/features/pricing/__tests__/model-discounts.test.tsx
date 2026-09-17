import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { getPricing, pricingQueryKey } from '../api'
import { ModelDiscountCaption } from '../components/model-discount-caption'
import { usePricingData } from '../hooks/use-pricing-data'
import { getDisplayGroupRatio, getModelGroupRatio } from '../lib/model-helpers'
import { formatFixedPrice, formatGroupPrice, formatPrice } from '../lib/price'
import type { PricingModel } from '../types'

const model: PricingModel = { id: 1, model_name: 'exact-model', quota_type: 0, model_ratio: 1, completion_ratio: 2, enable_groups: ['vip'], group_ratio: { vip: 2 } }
const user = (id: number) => ({ id, username: `user${id}`, role: 1 })
afterEach(() => { useAuthStore.getState().auth.reset(); vi.restoreAllMocks() })

describe('viewer model discounts', () => {
  it.each([0, 1, 0.7])('preserves factor %s and uses final multiplier without multiplying twice', (factor) => {
    const discounted = { ...model, model_group_ratio: { vip: 2 * factor }, model_discount: { model: model.model_name, source: 'user', factor, revision: 1 } }
    expect(getDisplayGroupRatio(discounted)).toBe(2 * factor)
    expect(getModelGroupRatio(discounted, 'vip', { vip: 20 })).toBe(2 * factor)
    expect(formatGroupPrice(discounted, 'vip', 'input', 'M', false, 1, 1, { vip: 20 })).toBe(formatPrice({ ...model, group_ratio: { vip: 2 * factor } }, 'input', 'M'))
    if (factor === 0) expect(formatFixedPrice({ ...discounted, quota_type: 1, model_price: 5 }, 'vip', false, 1, 1, { vip: 20 })).toMatch(/0/)
    render(<ModelDiscountCaption model={discounted} />)
    expect(screen.getByText(/个人专属/)).toHaveTextContent(factor === 1 ? '原价' : `${factor * 10}折`)
  })

  it('uses personalized group multipliers for models enabled for all groups', () => {
    const allGroups = { ...model, enable_groups: ['all'], model_group_ratio: { vip: 0, paid: 0.6 } }
    expect(getDisplayGroupRatio(allGroups)).toBe(0)
    expect(getDisplayGroupRatio(allGroups, 'paid')).toBe(0.6)
  })

  it('retains legacy fallback without fields and separates anonymous/user/session query keys', () => {
    expect(getDisplayGroupRatio(model)).toBe(2)
    expect(pricingQueryKey()).not.toEqual(pricingQueryKey(1))
    expect(pricingQueryKey(1, 'a')).not.toEqual(pricingQueryKey(2, 'a'))
    expect(pricingQueryKey(1, 'a')).not.toEqual(pricingQueryKey(1, 'b'))
  })

  it('rejects an in-flight response after account switch and disables HTTP deduplication', async () => {
    useAuthStore.getState().auth.setUser(user(1))
    let finish!: (result: unknown) => void
    const get = vi.spyOn(api, 'get').mockImplementation(() => new Promise((resolve) => { finish = resolve }) as ReturnType<typeof api.get>)
    const request = getPricing()
    useAuthStore.getState().auth.setUser(user(2))
    finish({ data: { success: true } })
    await expect(request).rejects.toThrow('Pricing identity changed')
    expect(get).toHaveBeenCalledWith('/api/pricing', expect.objectContaining({ disableDuplicate: true }))
  })

  it('clears previous identity data on switch and ignores personalized fields for anonymous viewers', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    client.setQueryData(['status'], { price: 1 })
    const response = { success: true, data: [model], vendors: [], group_ratio: { vip: 2 }, model_group_ratio: { 'exact-model': { vip: 0 } }, model_discounts: { 'exact-model': { factor: 0, source: 'user', revision: 1, model: 'exact-model' } } }
    vi.spyOn(api, 'get').mockResolvedValue({ data: response })
    useAuthStore.getState().auth.setUser(user(1))
    const hook = renderHook(() => usePricingData(), { wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
    await waitFor(() => expect(hook.result.current.models).toHaveLength(1))
    expect(getDisplayGroupRatio(hook.result.current.models[0])).toBe(0)
    act(() => useAuthStore.getState().auth.reset())
    await waitFor(() => expect(hook.result.current.models).toHaveLength(1))
    expect(getDisplayGroupRatio(hook.result.current.models[0])).toBe(2)
    expect(hook.result.current.models[0].model_discount).toBeUndefined()
    expect(client.getQueryData(pricingQueryKey(1))).toBeUndefined()
    expect(model).not.toHaveProperty('model_discount')
    hook.unmount()
    client.clear()
  })
})
