import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { AiccAssets } from '../index'
import * as api from '../api'
import type { Asset, AssetGroup } from '../types'
import { PAGE_SIZE } from '../use-paged-list'

// Exercise real slot extraction and scroll layout, not a div substitute.
vi.mock('@/components/layout', async () => import('@/components/layout/components/section-page-layout'))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))
vi.mock('../api', () => ({
  listAssetGroups: vi.fn(), listAssets: vi.fn(), createH5Session: vi.fn(),
  queryGroupByBytedToken: vi.fn(), createAssetGroup: vi.fn(), createAsset: vi.fn(),
  deleteAssetGroup: vi.fn(), deleteAsset: vi.fn(),
}))
const group = (id: string): AssetGroup => ({ groupId: id, groupName: `组${id}`, groupType: 'AIGC' })
const asset = (id: string, status = 'ACTIVE', assetType: Asset['assetType'] = 'Image'): Asset => ({
  assetId: id, assetName: `素材${id}`, groupId: '1', assetType, status, assetUrl: `https://example.com/${id}`,
})
const page = <T,>(data: T[], pageNo = 1, total?: number): api.PageResult<T> => ({ data, pageNo, total, pageSize: PAGE_SIZE })
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.listAssetGroups).mockResolvedValue(page([group('1')]))
  vi.mocked(api.listAssets).mockResolvedValue(page([asset('1')]))
})

it('paginates both lists using backend totals and resets assets when a group page changes', async () => {
  const user = userEvent.setup()
  vi.mocked(api.listAssetGroups).mockImplementation(async (params) => page([group(params.pageNo === 2 ? '2' : '1')], params.pageNo, 13))
  vi.mocked(api.listAssets).mockImplementation(async (params) => page([asset(`${params.groupIds}-${params.pageNo}`)], params.pageNo, 13))
  render(<AiccAssets />)
  await screen.findByRole('article', { name: '素材1-1' })
  await user.click(screen.getByRole('button', { name: '素材下一页' }))
  await screen.findByRole('article', { name: '素材1-2' })
  expect(screen.getByRole('button', { name: '素材下一页' })).toBeDisabled()
  expect(api.listAssets).toHaveBeenLastCalledWith(expect.objectContaining({ pageNo: 2, pageSize: PAGE_SIZE }), expect.any(AbortSignal))
  await user.click(screen.getByRole('button', { name: '素材组下一页' }))
  await screen.findByRole('article', { name: '素材2-1' })
  expect(api.listAssetGroups).toHaveBeenLastCalledWith(expect.objectContaining({ pageNo: 2, pageSize: PAGE_SIZE }), expect.any(AbortSignal))
  expect(api.listAssets).toHaveBeenLastCalledWith(expect.objectContaining({ groupIds: '2', pageNo: 1 }), expect.any(AbortSignal))
  await user.click(screen.getByRole('button', { name: '素材组上一页' }))
  await screen.findByRole('article', { name: '素材1-1' })
})

it('probes full pages without totals and stops on empty/short pages with a way back', async () => {
  const user = userEvent.setup()
  vi.mocked(api.listAssetGroups).mockImplementation(async (params) => page(params.pageNo === 1 ? Array.from({ length: PAGE_SIZE }, (_, i) => group(String(i))) : [], params.pageNo))
  vi.mocked(api.listAssets).mockImplementation(async (params) => page(params.pageNo === 1 ? Array.from({ length: PAGE_SIZE }, (_, i) => asset(String(i))) : [asset('last')], params.pageNo))
  render(<AiccAssets />)
  await screen.findByRole('article', { name: '素材0' })
  await user.click(screen.getByRole('button', { name: '素材下一页' }))
  await screen.findByRole('article', { name: '素材last' })
  expect(screen.getByRole('button', { name: '素材下一页' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '素材组下一页' }))
  await screen.findByText('本页暂无素材组，可返回上一页')
  expect(screen.getByRole('button', { name: '素材组下一页' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '素材组上一页' })).toBeEnabled()
  expect(within(screen.getByRole('navigation', { name: '素材组分页' })).getByText(/总数未知/)).toBeVisible()
})

it('aborts old group requests on tab change and ignores their late results', async () => {
  const user = userEvent.setup()
  const old = deferred<api.PageResult<AssetGroup>>()
  vi.mocked(api.listAssetGroups).mockImplementation((params) => params.groupType === 'LivenessFace' ? old.promise : Promise.resolve(page([group('new')], 1, 1)))
  render(<AiccAssets />)
  const signal = vi.mocked(api.listAssetGroups).mock.calls[0][1]
  await user.click(screen.getByRole('tab', { name: '虚拟人像素材库 (AIGC)' }))
  await screen.findByRole('button', { name: /组new/ })
  expect(signal?.aborted).toBe(true)
  await act(async () => old.resolve(page([group('old')])))
  expect(screen.queryByRole('button', { name: /组old/ })).not.toBeInTheDocument()
})

it('ignores late asset errors after selection changes and cancels requests at unmount', async () => {
  const user = userEvent.setup()
  const old = deferred<api.PageResult<Asset>>()
  const last = deferred<api.PageResult<Asset>>()
  vi.mocked(api.listAssetGroups).mockResolvedValue(page([group('1'), group('2')]))
  vi.mocked(api.listAssets).mockImplementation((params) => params.groupIds === '1' ? old.promise : last.promise)
  const { unmount } = render(<AiccAssets />)
  await waitFor(() => expect(api.listAssets).toHaveBeenCalledTimes(1))
  const oldSignal = vi.mocked(api.listAssets).mock.calls[0][1]
  await user.click(screen.getByRole('button', { name: /组2/ }))
  await waitFor(() => expect(api.listAssets).toHaveBeenCalledTimes(2))
  expect(oldSignal?.aborted).toBe(true)
  await act(async () => old.reject(new Error('stale error')))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByText('正在加载素材…')).toBeVisible()
  const lastSignal = vi.mocked(api.listAssets).mock.calls[1][1]
  unmount()
  expect(lastSignal?.aborted).toBe(true)
  await act(async () => last.resolve(page([asset('late')])) )
  expect(toast.error).not.toHaveBeenCalled()
})

it('ignores old page results during a refresh', async () => {
  const user = userEvent.setup()
  const old = deferred<api.PageResult<Asset>>()
  vi.mocked(api.listAssets).mockResolvedValueOnce(page([asset('first')], 1, 13)).mockImplementationOnce(() => old.promise).mockResolvedValue(page([asset('new')]))
  render(<AiccAssets />)
  await screen.findByRole('article', { name: '素材first' })
  await user.click(screen.getByRole('button', { name: '素材下一页' }))
  await user.click(screen.getByRole('button', { name: '刷新素材组' }))
  await screen.findByRole('article', { name: '素材new' })
  await act(async () => old.resolve(page([asset('old')], 2)))
  expect(screen.queryByRole('article', { name: '素材old' })).not.toBeInTheDocument()
})

it('distinguishes loading, errors, retry and empty results', async () => {
  const user = userEvent.setup()
  const pending = deferred<api.PageResult<AssetGroup>>()
  vi.mocked(api.listAssetGroups).mockReturnValueOnce(pending.promise).mockResolvedValue(page([]))
  render(<AiccAssets />)
  expect(screen.getByText('正在加载素材组…')).toBeVisible()
  expect(screen.queryByText('暂无素材组')).not.toBeInTheDocument()
  await act(async () => pending.reject(new Error('权限不足')))
  expect(screen.getByRole('alert')).toHaveTextContent('权限不足')
  expect(screen.queryByText('暂无素材组')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '重试' }))
  await screen.findByText('暂无素材组')
})

it('shows asset errors separately from empty assets', async () => {
  const user = userEvent.setup()
  vi.mocked(api.listAssets).mockRejectedValueOnce(new Error('上游响应异常')).mockResolvedValue(page([]))
  render(<AiccAssets />)
  expect(await screen.findByRole('alert')).toHaveTextContent('上游响应异常')
  expect(screen.queryByText('该素材组下暂无入库素材')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '重试' }))
  await screen.findByText('该素材组下暂无入库素材')
})

it('copies only ACTIVE assets, awaits clipboard success, and reports clipboard failure', async () => {
  const user = userEvent.setup()
  const pending = deferred<void>()
  const write = vi.spyOn(navigator.clipboard, 'writeText').mockImplementationOnce(() => pending.promise).mockRejectedValueOnce(new Error('denied'))
  vi.mocked(api.listAssets).mockResolvedValue(page([asset('ok'), ...['FAILED', 'EXPIRED', 'PROCESSING', 'UNKNOWN', 'UNRECOGNIZED'].map((status) => asset(status, status))]))
  render(<AiccAssets />)
  await screen.findByRole('article', { name: '素材ok' })
  for (const status of ['FAILED', 'EXPIRED', 'PROCESSING', 'UNKNOWN', 'UNRECOGNIZED']) {
    const button = screen.getByRole('button', { name: `复制 素材${status} URI` })
    expect(button).toBeDisabled()
    await user.click(button)
  }
  expect(write).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: '复制 素材ok URI' }))
  expect(write).toHaveBeenCalledWith('asset://ok')
  expect(toast.success).not.toHaveBeenCalled()
  await act(async () => pending.resolve())
  expect(toast.success).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('button', { name: '复制 素材ok URI' }))
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('复制失败')))
  expect(toast.success).toHaveBeenCalledTimes(1)
  expect(screen.queryByText('已复制 URI')).not.toBeInTheDocument()
})

it('uses real image/video/audio previews and displays expired-link errors', async () => {
  vi.mocked(api.listAssets).mockResolvedValue(page([asset('image'), asset('video', 'ACTIVE', 'Video'), asset('audio', 'ACTIVE', 'Audio')]))
  render(<AiccAssets />)
  const image = await screen.findByRole('img', { name: '素材image' })
  expect(screen.getByLabelText('素材video视频预览').tagName).toBe('VIDEO')
  expect(screen.getByLabelText('素材audio音频预览').tagName).toBe('AUDIO')
  fireEvent.error(image)
  expect(screen.getByText('预览加载失败，链接可能已失效')).toBeVisible()
  // Layout contract only: jsdom cannot verify physical scroll/overflow geometry.
  const main = screen.getByRole('main')
  expect(main.querySelector('.overflow-auto')).toContainElement(screen.getByRole('navigation', { name: '素材分页' }))
})
