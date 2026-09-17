import { beforeEach, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { createAsset, deleteAsset, listAssetGroups, listAssets } from '../api'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn(), delete: vi.fn() } }))
const group = { groupId: 'g', groupName: '组', groupType: 'AIGC' }
const asset = { assetId: 'a', assetName: '素材', groupId: 'g', assetType: 'Image' }
beforeEach(() => vi.resetAllMocks())

it.each(['data', 'list'])('accepts body.%s with pagination and forwards abort', async (key) => {
  vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: { body: { [key]: [group], total: '25', pageNo: 2, pageSize: 12 } } } })
  const signal = new AbortController().signal
  expect(await listAssetGroups({ pageNo: 2, pageSize: 12 }, signal)).toEqual({ data: [group], total: 25, pageNo: 2, pageSize: 12 })
  expect(api.get).toHaveBeenCalledWith('/api/aicc/asset-groups', { params: { pageNo: 2, pageSize: 12 }, signal })
})

it('preserves missing totals and request pagination for direct arrays', async () => {
  vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: [asset] } })
  expect(await listAssets({ pageNo: 3, pageSize: 12 })).toEqual({ data: [asset], total: undefined, pageNo: 3, pageSize: 12 })
})

it('supports direct list objects and outer pagination', async () => {
  vi.mocked(api.get).mockResolvedValueOnce({ data: { success: true, data: { list: [], total: 0 } } })
  expect((await listAssets({})).total).toBe(0)
  vi.mocked(api.get).mockResolvedValueOnce({ data: { success: true, data: { body: [asset], pageNo: 2, pageSize: 5, total: 8 } } })
  expect(await listAssets({})).toEqual({ data: [asset], total: 8, pageNo: 2, pageSize: 5 })
})

it.each([
  { success: false, message: '权限不足', data: [] },
  { success: true, data: { success: false, message: '上游失败' } },
  { success: true, data: { body: { success: false, message: '上游失败' } } },
  { data: [] },
  { success: true, data: { unexpected: [] } },
  { success: true, data: { body: null } },
  { success: true, data: { list: [null] } },
  { success: true, data: { list: [], total: -1 } },
  { success: true, data: { list: [], pageSize: 0 } },
])('rejects invalid/error envelope rather than showing an empty list: %j', async (envelope) => {
  vi.mocked(api.get).mockResolvedValue({ data: envelope })
  await expect(listAssetGroups({})).rejects.toThrow()
  await expect(listAssets({})).rejects.toThrow()
})

it('accepts unnamed remote groups without discarding their identity', async () => {
  vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: { body: { data: [{ groupId: 'group-real', groupType: 'LivenessFace', groupName: null }], total: 1 } } } })
  const result = await listAssetGroups({})
  expect(result.data[0].groupId).toBe('group-real')
  expect(result.data[0].groupName).toBe('未命名素材')
})

it('does not report mutations as successful for success:false', async () => {
  const response = { data: { success: false, message: '拒绝操作' } }
  vi.mocked(api.post).mockResolvedValue(response)
  vi.mocked(api.delete).mockResolvedValue(response)
  await expect(createAsset({ groupId: 'g', assetName: 'a', assetType: 'Image', assetUrl: 'https://example.com/a.png' })).rejects.toThrow('拒绝操作')
  await expect(deleteAsset('a')).rejects.toThrow('拒绝操作')
})
