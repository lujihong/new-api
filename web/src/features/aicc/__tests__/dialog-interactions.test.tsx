import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { AiccAssets } from '../index'
import * as api from '../api'
import { CreateAssetGroupDialog } from '../components/create-asset-group-dialog'
import { RealPersonAuthDialog } from '../components/real-person-auth-dialog'

// Keep the actual slot layout: replacing it with a div hides dropped dialogs.
vi.mock('@/components/layout', async () => import('@/components/layout/components/section-page-layout'))
vi.mock('../api', () => ({
  listChannels: vi.fn(), listAssetGroups: vi.fn(), listAssets: vi.fn(), createH5Session: vi.fn(),
  queryGroupByBytedToken: vi.fn(), createAssetGroup: vi.fn(), createAsset: vi.fn(),
  deleteAssetGroup: vi.fn(), deleteAsset: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(api.listChannels).mockResolvedValue([{ id: 7, name: '专线一', region: '北京', models: [] }])
  vi.mocked(api.listAssetGroups).mockResolvedValue({ data: [{ groupId: 'group-test', groupType: 'AIGC', groupName: '测试组' }], pageNo: 1, pageSize: 12 })
  vi.mocked(api.listAssets).mockResolvedValue({ data: [], pageNo: 1, pageSize: 12 })
  vi.mocked(api.createH5Session).mockResolvedValue({ bytedToken: 'test-token', h5Link: 'https://example.com/auth', expiresIn: 1800 })
})

it('opens and dismisses the real authentication dialog from the page', async () => {
  const user = userEvent.setup()
  render(<AiccAssets />)
  await user.click(await screen.findByRole('button', { name: '真人实名认证 (扫码)' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await waitFor(() => expect(api.createH5Session).toHaveBeenCalledTimes(1))
  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

it('opens create-group and add-asset dialogs and cancels without mutation', async () => {
  const user = userEvent.setup()
  render(<AiccAssets />)
  await user.click(await screen.findByRole('tab', { name: '虚拟人像素材库 (AIGC)' }))
  await user.click(screen.getByRole('button', { name: '新建虚拟素材组' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await user.click(screen.getByRole('button', { name: '取消' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  await user.click(await screen.findByRole('button', { name: '添加素材' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await user.click(screen.getByRole('button', { name: '取消' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(api.createAssetGroup).not.toHaveBeenCalled()
  expect(api.createAsset).not.toHaveBeenCalled()
})

it('aborts create-group on channel switch and ignores a late success', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(api.createAssetGroup).mockReturnValueOnce(new Promise(done => { resolve = done }))
  const user = userEvent.setup()
  const onSuccess = vi.fn()
  const onOpenChange = vi.fn()
  const view = render(<CreateAssetGroupDialog channelId={7} open onSuccess={onSuccess} onOpenChange={onOpenChange} />)
  await user.type(screen.getByLabelText('素材组名称'), '新组')
  await user.click(screen.getByRole('button', { name: '确认创建' }))
  expect(api.createAssetGroup).toHaveBeenCalledWith(expect.objectContaining({ groupName: '新组' }), 7, expect.any(AbortSignal))
  const signal = vi.mocked(api.createAssetGroup).mock.calls.at(-1)?.[2]
  view.rerender(<CreateAssetGroupDialog channelId={8} open onSuccess={onSuccess} onOpenChange={onOpenChange} />)
  expect(signal?.aborted).toBe(true)
  await act(async () => resolve({}))
  expect(onSuccess).not.toHaveBeenCalled()
  expect(onOpenChange).not.toHaveBeenCalled()
  expect(screen.getByLabelText('素材组名称')).toHaveValue('')
})

it('aborts the old auth session on channel switch and keeps the new channel link', async () => {
  let resolve!: (value: Awaited<ReturnType<typeof api.createH5Session>>) => void
  vi.mocked(api.createH5Session).mockReturnValueOnce(new Promise(done => { resolve = done }))
    .mockResolvedValueOnce({ bytedToken: 'new-token', h5Link: 'https://example.com/new', expiresIn: 60 })
  const props = { open: true, onOpenChange: vi.fn(), onSuccess: vi.fn() }
  const view = render(<RealPersonAuthDialog {...props} channelId={7} />)
  await waitFor(() => expect(api.createH5Session).toHaveBeenCalledWith(7, expect.any(AbortSignal)))
  const signal = vi.mocked(api.createH5Session).mock.calls.at(-1)?.[1]
  view.rerender(<RealPersonAuthDialog {...props} channelId={8} />)
  expect(await screen.findByLabelText('本人认证链接')).toHaveValue('https://example.com/new')
  expect(signal?.aborted).toBe(true)
  expect(api.createH5Session).toHaveBeenLastCalledWith(8, expect.any(AbortSignal))
  await act(async () => resolve({ bytedToken: 'old-token', h5Link: 'https://example.com/old', expiresIn: 60 }))
  expect(screen.getByLabelText('本人认证链接')).toHaveValue('https://example.com/new')
  expect(props.onSuccess).not.toHaveBeenCalled()
})
