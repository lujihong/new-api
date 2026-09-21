import { beforeEach, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { createAsset, createAssetGroup, createH5Session, deleteAsset, deleteAssetGroup, listAssetGroups, listAssets, uploadAsset, queryGroupByBytedToken } from '../api'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn(), delete: vi.fn() } }))
beforeEach(() => vi.resetAllMocks())

it.each(['personal', 'management'] as const)('builds %s paths, never a scope query, and bypasses shared GET deduplication', async (scope) => {
  vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: [] } })
  vi.mocked(api.delete).mockResolvedValue({ data: { success: true, data: {} } })
  const prefix = scope === 'management' ? '/api/aicc/admin' : '/api/aicc'
  const signal = new AbortController().signal
  await listAssetGroups({ channelId: 7, pageNo: 1, scope }, signal)
  expect(api.get).toHaveBeenLastCalledWith(`${prefix}/asset-groups`, { params: { channel_id: 7, pageNo: 1 }, signal, disableDuplicate: true })
  await listAssets({ channelId: 7, groupIds: 'g', scope }, signal)
  expect(api.get).toHaveBeenLastCalledWith(`${prefix}/assets`, { params: { channel_id: 7, groupIds: 'g' }, signal, disableDuplicate: true })
  await deleteAssetGroup('g/a', 7, scope, signal)
  expect(api.delete).toHaveBeenLastCalledWith(`${prefix}/asset-groups/g%2Fa`, { params: { channel_id: 7 }, signal })
  await deleteAsset('a/b', 7, scope, signal)
  expect(api.delete).toHaveBeenLastCalledWith(`${prefix}/assets/a%2Fb`, { params: { channel_id: 7 }, signal })
})

it('uploads multipart file/group/type with timeout and abort, without manually setting Content-Type', async () => {
  const data = { id: 'u', url: 'https://example.com/file', assetType: 'Image', mimeType: 'image/jpeg', bytes: 4, expiresAt: Date.now() + 60_000 }
  vi.mocked(api.post).mockResolvedValue({ data: { success: true, data } })
  const file = new File(['file'], 'file.jpg', { type: 'image/jpeg' })
  const signal = new AbortController().signal
  expect(await uploadAsset(file, 'g', 'Image', 7, signal)).toEqual(data)
  const [path, form, config] = vi.mocked(api.post).mock.calls[0]
  expect(path).toBe('/api/aicc/uploads')
  expect(form).toBeInstanceOf(FormData)
  expect((form as FormData).get('file')).toBe(file)
  expect((form as FormData).get('groupId')).toBe('g')
  expect((form as FormData).get('assetType')).toBe('Image')
  expect(config).toMatchObject({ params: { channel_id: 7 }, signal, timeout: 120_000, skipErrorHandler: true, skipBusinessError: true })
  expect(config?.headers).toBeUndefined()
})

it.each([
  { success: false, message: '上传被拒绝' },
  { success: true, data: {} },
  { success: true, data: { id: 'u', url: 'https://example.com', assetType: 'Image', mimeType: 'image/jpeg', bytes: 4, expiresAt: 1 } },
])('rejects failed or expired uploads: %j', async (data) => {
  vi.mocked(api.post).mockResolvedValue({ data })
  await expect(uploadAsset(new File(['x'], 'x.jpg'), 'g', 'Image', 7)).rejects.toThrow()
})

it('keeps create and authentication personal and forwards create abort/timeout', async () => {
  vi.mocked(api.post).mockResolvedValue({ data: { success: true, data: {} } })
  const signal = new AbortController().signal
  const payload = { groupId: 'g', assetName: 'a', assetUrl: 'https://example.com/a', assetType: 'Image' }
  await createAsset(payload, 7, signal)
  expect(api.post).toHaveBeenLastCalledWith('/api/aicc/assets', payload, expect.objectContaining({ params: { channel_id: 7 }, signal, timeout: 60_000 }))
  await createAssetGroup({ groupName: '组' }, 7, signal)
  expect(api.post).toHaveBeenLastCalledWith('/api/aicc/asset-groups', { groupName: '组' }, { params: { channel_id: 7 }, signal })
  vi.mocked(api.post).mockResolvedValue({ data: { success: true, data: { bytedToken: 't', h5Link: 'https://example.com/auth', expiresIn: 60 } } })
  await createH5Session(7, signal)
  expect(api.post).toHaveBeenLastCalledWith('/api/aicc/auth/session', undefined, { params: { channel_id: 7 }, signal })
})

it('queries auth results using the server-bound session without a channel override', async () => {
  vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: { authenticated: true } } })
  const signal = new AbortController().signal
  expect(await queryGroupByBytedToken('t/a', signal)).toEqual({ authenticated: true })
  expect(api.get).toHaveBeenCalledWith('/api/aicc/auth/session/t%2Fa', { signal })
})
