import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth-store'
import { AiccAssets } from '../index'
import * as api from '../api'
import type { AssetGroup } from '../types'

vi.mock('@/components/layout', async () => import('@/components/layout/components/section-page-layout'))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))
vi.mock('../api', () => ({ listAssetGroups: vi.fn(), listAssets: vi.fn(), deleteAssetGroup: vi.fn(), deleteAsset: vi.fn(), createH5Session: vi.fn(), queryGroupByBytedToken: vi.fn(), createAssetGroup: vi.fn(), createAsset: vi.fn() }))
const page = (id = 'g'): api.PageResult<AssetGroup> => ({ data: [{ groupId: id, groupName: `组${id}`, groupType: 'AIGC' }], pageNo: 1, pageSize: 12 })
function login(id = 1, role = 10) { useAuthStore.getState().auth.setUser({ id, role, username: `user-${id}` }) }
beforeEach(() => {
  vi.resetAllMocks()
  login()
  vi.mocked(api.listAssetGroups).mockResolvedValue(page())
  vi.mocked(api.listAssets).mockResolvedValue({ data: [{ assetId: 'a', groupId: 'g', assetName: '照片', assetType: 'Image', status: 'ACTIVE' }], pageNo: 1, pageSize: 12 })
})
afterEach(() => { vi.restoreAllMocks(); useAuthStore.getState().auth.setUser(null) })

it.each([10, 100])('defaults admin/root role %s to personal for both real and virtual assets', async (role) => {
  login(1, role)
  const user = userEvent.setup()
  render(<AiccAssets />)
  await screen.findByRole('article', { name: '照片' })
  expect(screen.getByRole('heading', { name: '我的素材' })).toBeVisible()
  expect(api.listAssetGroups).toHaveBeenLastCalledWith(expect.objectContaining({ scope: 'personal', groupType: 'LivenessFace' }), expect.any(AbortSignal))
  await user.click(screen.getByRole('tab', { name: '虚拟人像素材库 (AIGC)' }))
  await screen.findByRole('article', { name: '照片' })
  expect(api.listAssetGroups).toHaveBeenLastCalledWith(expect.objectContaining({ scope: 'personal', groupType: 'AIGC' }), expect.any(AbortSignal))
  expect(screen.getByRole('button', { name: '新建虚拟素材组' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: '管理全部素材' }))
  await screen.findByRole('article', { name: '照片' })
  expect(screen.getByRole('note')).toHaveTextContent('不会自动认领')
  expect(api.listAssets).toHaveBeenLastCalledWith(expect.objectContaining({ scope: 'management' }), expect.any(AbortSignal))
  expect(screen.queryByRole('button', { name: '新建虚拟素材组' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '真人实名认证 (扫码)' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '添加素材' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: '复制 照片 URI' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '返回我的素材' }))
  await screen.findByRole('article', { name: '照片' })
  expect(screen.getByRole('button', { name: '添加素材' })).toBeEnabled()
  expect(screen.getByRole('button', { name: '复制 照片 URI' })).toBeEnabled()
  expect(api.listAssets).toHaveBeenLastCalledWith(expect.objectContaining({ scope: 'personal' }), expect.any(AbortSignal))
})

it('does not expose management to ordinary users', async () => {
  login(2, 1)
  render(<AiccAssets />)
  await screen.findByRole('article', { name: '照片' })
  expect(screen.queryByRole('button', { name: '管理全部素材' })).not.toBeInTheDocument()
  expect(api.listAssetGroups).toHaveBeenLastCalledWith(expect.objectContaining({ scope: 'personal' }), expect.any(AbortSignal))
})

it('requires second confirmation for management deletion and uses management scope', async () => {
  const user = userEvent.setup()
  const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(true).mockReturnValueOnce(false)
  render(<AiccAssets />)
  await user.click(screen.getByRole('button', { name: '管理全部素材' }))
  await screen.findByRole('article', { name: '照片' })
  await user.click(screen.getByRole('button', { name: '删除素材 照片' }))
  expect(confirm).toHaveBeenCalledTimes(2)
  expect(api.deleteAsset).not.toHaveBeenCalled()
  confirm.mockReturnValue(true)
  await user.click(screen.getByRole('button', { name: '删除素材 照片' }))
  await waitFor(() => expect(api.deleteAsset).toHaveBeenCalledWith('a', 'management'))
  await screen.findByRole('article', { name: '照片' })
  confirm.mockReturnValueOnce(true).mockReturnValueOnce(false)
  await user.click(screen.getByRole('button', { name: '删除素材组' }))
  expect(api.deleteAssetGroup).not.toHaveBeenCalled()
  confirm.mockReturnValue(true)
  await user.click(screen.getByRole('button', { name: '删除素材组' }))
  expect(api.deleteAssetGroup).toHaveBeenCalledWith('g', 'management')
})

it('aborts the previous scope and ignores its late results', async () => {
  let resolve!: (page: api.PageResult<AssetGroup>) => void
  vi.mocked(api.listAssetGroups).mockReturnValueOnce(new Promise((res) => { resolve = res })).mockResolvedValue(page('admin'))
  const user = userEvent.setup()
  render(<AiccAssets />)
  const signal = vi.mocked(api.listAssetGroups).mock.calls[0][1]!
  await user.click(screen.getByRole('button', { name: '管理全部素材' }))
  await screen.findByRole('button', { name: /组admin/ })
  expect(signal.aborted).toBe(true)
  await act(async () => resolve(page('old-personal')))
  expect(screen.queryByRole('button', { name: /组old-personal/ })).not.toBeInTheDocument()
})

it('remounts on auth.user.id changes and returns to personal without stale requests', async () => {
  const user = userEvent.setup()
  let resolve!: (page: api.PageResult<AssetGroup>) => void
  render(<AiccAssets />)
  await screen.findByRole('article', { name: '照片' })
  vi.mocked(api.listAssetGroups).mockReturnValueOnce(new Promise((res) => { resolve = res }))
  await user.click(screen.getByRole('button', { name: '管理全部素材' }))
  const signal = vi.mocked(api.listAssetGroups).mock.calls.at(-1)![1]!
  vi.mocked(api.listAssetGroups).mockResolvedValue(page('new-user'))
  act(() => login(2, 10))
  await screen.findByRole('button', { name: /组new-user/ })
  expect(signal.aborted).toBe(true)
  expect(screen.getByRole('heading', { name: '我的素材' })).toBeVisible()
  expect(api.listAssetGroups).toHaveBeenLastCalledWith(expect.objectContaining({ scope: 'personal' }), expect.any(AbortSignal))
  await act(async () => resolve(page('old-admin')))
  expect(screen.queryByRole('button', { name: /组old-admin/ })).not.toBeInTheDocument()
})
