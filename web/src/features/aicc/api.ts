import { api } from '@/lib/api'
import type { Asset, AssetGroup, H5SessionResponse } from './types'

export interface PageResult<T> {
  data: T[]
  total?: number
  pageNo: number
  pageSize: number
}

export type AssetScope = 'personal' | 'management'

function scopePath(resource: 'asset-groups' | 'assets', scope: AssetScope = 'personal') {
  return `/api/aicc/${scope === 'management' ? 'admin/' : ''}${resource}`
}

interface PageParams {
  scope?: AssetScope
  pageNo?: number
  pageSize?: number
  groupType?: string
}

type RecordValue = Record<string, unknown>
const isRecord = (value: unknown): value is RecordValue =>
  value !== null && typeof value === 'object' && !Array.isArray(value)

function checkFailure(value: unknown) {
  if (isRecord(value) && typeof value.state === 'string' && value.state !== 'OK') {
    throw new Error('移动云素材响应异常，请刷新重试')
  }
  if (isRecord(value) && value.success === false) {
    throw new Error(typeof value.message === 'string' && value.message ? value.message : 'AICC 请求失败')
  }
}

function unwrapEnvelope(value: unknown): unknown {
  checkFailure(value)
  if (!isRecord(value) || value.success !== true) {
    throw new Error('AICC 响应格式异常：缺少有效的 success 标记')
  }
  checkFailure(value.data)
  return value.data
}

function pageNumber(value: unknown, fallback: number | undefined, name: string, min: number) {
  if (value === undefined) return fallback
  const number = typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : value
  if (typeof number !== 'number' || !Number.isSafeInteger(number) || number < min) {
    throw new Error(`AICC 分页格式异常：${name}`)
  }
  return number
}

function parsePage<T>(value: unknown, params: PageParams, idKey: string, nameKey: string): PageResult<T> {
  const payload = unwrapEnvelope(value)
  const body = isRecord(payload) && 'body' in payload ? payload.body : payload
  checkFailure(body)
  const rows = Array.isArray(body) ? body : isRecord(body) ? (body.data ?? body.list) : undefined
  if (!Array.isArray(rows) || !rows.every((row: unknown) =>
    isRecord(row) && typeof row[idKey] === 'string' && Boolean(row[idKey]) && (row[nameKey] == null || typeof row[nameKey] === 'string')
  )) {
    throw new Error('AICC 列表格式异常：无法识别素材列表')
  }
  const metadata = isRecord(body) ? body : {}
  const parent = isRecord(payload) ? payload : {}
  return {
    data: rows.map(row => ({ ...row, [nameKey]: row[nameKey] || '未命名素材' })) as T[],
    total: pageNumber(metadata.total ?? parent.total, undefined, 'total', 0),
    pageNo: pageNumber(metadata.pageNo ?? parent.pageNo, params.pageNo ?? 1, 'pageNo', 1)!,
    pageSize: pageNumber(metadata.pageSize ?? parent.pageSize, params.pageSize ?? 20, 'pageSize', 1)!,
  }
}

export async function createH5Session(signal?: AbortSignal): Promise<H5SessionResponse> {
  const res = await api.post('/api/aicc/auth/session', undefined, { signal })
  const data = unwrapEnvelope(res.data)
  if (!isRecord(data) || typeof data.bytedToken !== 'string' || typeof data.h5Link !== 'string' ||
    !data.bytedToken || !/^https?:\/\//i.test(data.h5Link) || typeof data.expiresIn !== 'number') {
    throw new Error('AICC 认证会话格式异常')
  }
  return data as unknown as H5SessionResponse
}

export async function queryGroupByBytedToken(token: string, signal?: AbortSignal): Promise<{ authenticated?: boolean }> {
  const res = await api.get(`/api/aicc/auth/session/${encodeURIComponent(token)}`, { signal })
  const data = unwrapEnvelope(res.data)
  if (!isRecord(data)) throw new Error('AICC 认证结果格式异常')
  return { authenticated: data.authenticated === true }
}

export async function listAssetGroups(params: PageParams, signal?: AbortSignal): Promise<PageResult<AssetGroup>> {
  const { scope, ...query } = params
  const res = await api.get(scopePath('asset-groups', scope), { params: query, signal, ...(scope ? { disableDuplicate: true } : {}) })
  return parsePage<AssetGroup>(res.data, params, 'groupId', 'groupName')
}

export async function createAssetGroup(data: { groupName: string; description?: string }): Promise<unknown> {
  const res = await api.post('/api/aicc/asset-groups', data)
  return unwrapEnvelope(res.data)
}

export async function deleteAssetGroup(groupId: string, scope: AssetScope = 'personal'): Promise<unknown> {
  const res = await api.delete(`${scopePath('asset-groups', scope)}/${encodeURIComponent(groupId)}`)
  return unwrapEnvelope(res.data)
}

export async function listAssets(params: PageParams & {
  groupIds?: string
  assetName?: string
  statuses?: string
}, signal?: AbortSignal): Promise<PageResult<Asset>> {
  const { scope, ...query } = params
  const res = await api.get(scopePath('assets', scope), { params: query, signal, ...(scope ? { disableDuplicate: true } : {}) })
  return parsePage<Asset>(res.data, params, 'assetId', 'assetName')
}

export async function createAsset(data: {
  groupId: string
  assetName: string
  assetUrl: string
  assetType: string
}, signal?: AbortSignal): Promise<unknown> {
  const res = await api.post('/api/aicc/assets', data, { signal, timeout: 60_000, skipErrorHandler: true, skipBusinessError: true })
  return unwrapEnvelope(res.data)
}

export async function deleteAsset(assetId: string, scope: AssetScope = 'personal'): Promise<unknown> {
  const res = await api.delete(`${scopePath('assets', scope)}/${encodeURIComponent(assetId)}`)
  return unwrapEnvelope(res.data)
}

export interface UploadedAsset {
  id: string
  url: string
  assetType: Asset['assetType']
  mimeType: string
  bytes: number
  expiresAt: string | number
}

export function uploadExpiry(expiresAt: UploadedAsset['expiresAt']): number {
  if (typeof expiresAt === 'number') return expiresAt < 1e12 ? expiresAt * 1000 : expiresAt
  return Date.parse(expiresAt)
}

export async function uploadAsset(file: File, groupId: string, assetType: Asset['assetType'], signal?: AbortSignal): Promise<UploadedAsset> {
  const form = new FormData()
  form.append('file', file)
  form.append('groupId', groupId)
  form.append('assetType', assetType)
  // Browser supplies multipart Content-Type including its boundary.
  const res = await api.post('/api/aicc/uploads', form, { signal, timeout: 120_000, skipErrorHandler: true, skipBusinessError: true })
  const data = unwrapEnvelope(res.data)
  if (!isRecord(data) || typeof data.id !== 'string' || !data.id || typeof data.url !== 'string' ||
    !/^https?:\/\//i.test(data.url) || data.assetType !== assetType || typeof data.mimeType !== 'string' ||
    typeof data.bytes !== 'number' || (typeof data.expiresAt !== 'string' && typeof data.expiresAt !== 'number') ||
    !Number.isFinite(uploadExpiry(data.expiresAt)) || uploadExpiry(data.expiresAt) <= Date.now()) {
    throw new Error('上传响应无效或链接已过期，请重新上传')
  }
  return data as unknown as UploadedAsset
}
