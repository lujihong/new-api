import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { AiccAssets } from '../index'
import * as api from '../api'

// Keep the actual slot layout: replacing it with a div hides dropped dialogs.
vi.mock('@/components/layout', async () => import('@/components/layout/components/section-page-layout'))
vi.mock('../api', () => ({
  listAssetGroups: vi.fn(), listAssets: vi.fn(), createH5Session: vi.fn(),
  queryGroupByBytedToken: vi.fn(), createAssetGroup: vi.fn(), createAsset: vi.fn(),
  deleteAssetGroup: vi.fn(), deleteAsset: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(api.listAssetGroups).mockResolvedValue({ data: [{ groupId: 'group-test', groupType: 'AIGC', groupName: '测试组' }], pageNo: 1, pageSize: 12 })
  vi.mocked(api.listAssets).mockResolvedValue({ data: [], pageNo: 1, pageSize: 12 })
  vi.mocked(api.createH5Session).mockResolvedValue({ bytedToken: 'test-token', h5Link: 'https://example.com/auth', expiresIn: 1800 })
})

it('opens and dismisses the real authentication dialog from the page', async () => {
  const user = userEvent.setup()
  render(<AiccAssets />)
  await user.click(screen.getByRole('button', { name: '真人实名认证 (扫码)' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await waitFor(() => expect(api.createH5Session).toHaveBeenCalledTimes(1))
  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

it('opens create-group and add-asset dialogs and cancels without mutation', async () => {
  const user = userEvent.setup()
  render(<AiccAssets />)
  await user.click(screen.getByRole('tab', { name: '虚拟人像素材库 (AIGC)' }))
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
