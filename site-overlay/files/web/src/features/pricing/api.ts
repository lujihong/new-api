/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { PricingData } from './types'

// ----------------------------------------------------------------------------
// Pricing APIs
// ----------------------------------------------------------------------------

// Get model pricing data
export function pricingQueryKey(userId?: number | null, sessionId?: string | null) {
  return ['pricing', userId ?? 'anonymous', sessionId ?? 'no-session'] as const
}

export async function getPricing(signal?: AbortSignal): Promise<PricingData> {
  const identity = useAuthStore.getState().auth
  const userId = identity.user?.id
  const sessionId = identity.session?.sid
  // Viewer prices must never reuse an in-flight request from another identity.
  const res = await api.get<PricingData>('/api/pricing', {
    disableDuplicate: true,
    signal,
  })
  const current = useAuthStore.getState().auth
  if (current.user?.id !== userId || current.session?.sid !== sessionId) {
    throw new Error('Pricing identity changed')
  }
  if (!res.data.success) throw new Error(res.data.message || 'Failed to load pricing')
  return res.data
}
