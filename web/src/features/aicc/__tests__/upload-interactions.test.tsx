import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { CreateAssetDialog } from '../components/create-asset-dialog'
import * as api from '../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('../api', async (original) => ({ ...await original<typeof api>(), uploadAsset: vi.fn(), createAsset: vi.fn() }))
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const photo = () => new File(['photo'], 'portrait.jpg', { type: 'image/jpeg' })
const result = (): api.UploadedAsset => ({ id: 'upload-1', url: 'https://example.com/upload-1', assetType: 'Image', mimeType: 'image/jpeg', bytes: 5, expiresAt: Date.now() + 60_000 })
const props = () => ({ channelId: 7, open: true, groupId: 'g1', groupName: '我的组', onOpenChange: vi.fn(), onSuccess: vi.fn() })
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.uploadAsset).mockResolvedValue(result())
  vi.mocked(api.createAsset).mockResolvedValue({ status: 'PROCESSING' })
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('defaults to clickable local file input, derives an editable stem, and sends two distinct steps', async () => {
  const user = userEvent.setup()
  const upload = deferred<api.UploadedAsset>()
  const create = deferred<unknown>()
  vi.mocked(api.uploadAsset).mockReturnValue(upload.promise)
  vi.mocked(api.createAsset).mockReturnValue(create.promise)
  const callbacks = props()
  render(<CreateAssetDialog {...callbacks} />)
  expect(screen.getByLabelText('素材来源')).toHaveValue('file')
  expect(screen.queryByLabelText('公网素材 URL')).not.toBeInTheDocument()
  const file = photo()
  await user.upload(screen.getByLabelText('本地素材文件'), file)
  expect((screen.getByLabelText('本地素材文件') as HTMLInputElement).files?.[0]).toBe(file)
  expect(screen.getByLabelText('素材名称')).toHaveValue('portrait')
  await user.clear(screen.getByLabelText('素材名称'))
  await user.type(screen.getByLabelText('素材名称'), '新照片')
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  expect(screen.getByRole('button', { name: /第 1 步/ })).toBeDisabled()
  expect(api.uploadAsset).toHaveBeenCalledWith(file, 'g1', 'Image', 7, expect.any(AbortSignal))
  expect(api.createAsset).not.toHaveBeenCalled()
  const uploaded = result()
  await act(async () => upload.resolve(uploaded))
  expect(screen.getByRole('button', { name: /第 2 步/ })).toBeDisabled()
  expect(api.createAsset).toHaveBeenCalledWith({ groupId: 'g1', assetName: '新照片', assetUrl: uploaded.url, assetType: 'Image' }, 7, expect.any(AbortSignal))
  expect(toast.success).not.toHaveBeenCalled()
  await act(async () => create.resolve({ status: 'PROCESSING' }))
  expect(toast.success).toHaveBeenCalledWith(expect.stringContaining('并非已可用'))
  expect(callbacks.onSuccess).toHaveBeenCalledOnce()
  expect(callbacks.onOpenChange).toHaveBeenCalledWith(false)
})

it('keeps one clear selected state and permits remove then reselecting the same file', async () => {
  const user = userEvent.setup()
  render(<CreateAssetDialog {...props()} />)
  const file = photo()
  const input = screen.getByLabelText('本地素材文件') as HTMLInputElement
  const click = vi.spyOn(input, 'click')
  await user.click(screen.getByRole('button', { name: '选择文件' }))
  expect(click).toHaveBeenCalledOnce()
  await user.upload(input, file)
  expect(input.files?.[0]).toBe(file)
  expect(screen.getByLabelText('已选文件')).toHaveTextContent('portrait.jpg')
  expect(screen.queryByText('尚未选择文件')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: '更换文件' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: '移除文件' }))
  expect(input.files).toHaveLength(0)
  expect(screen.getByText('尚未选择文件')).toBeVisible()
  expect(screen.getByRole('button', { name: '确认添加' })).toBeDisabled()
  await user.upload(input, file)
  expect(input.files?.[0]).toBe(file)
  expect(screen.getByRole('button', { name: '确认添加' })).toBeEnabled()
})

it('select A, drop B, cancel picker, then select A uploads the actual final choice', async () => {
  const user = userEvent.setup()
  render(<CreateAssetDialog {...props()} />)
  const a = photo()
  const b = new File(['other'], 'other.png', { type: 'image/png' })
  const input = screen.getByLabelText('本地素材文件') as HTMLInputElement
  await user.upload(input, a)
  fireEvent.drop(screen.getByLabelText('文件拖放区'), { dataTransfer: { files: [b] } })
  expect(input.files).toHaveLength(0)
  expect(screen.getByLabelText('已选文件')).toHaveTextContent('other.png')
  fireEvent(input, new Event('cancel', { bubbles: true }))
  expect(screen.getByLabelText('已选文件')).toHaveTextContent('other.png')
  await user.upload(input, a)
  expect(input.files?.[0]).toBe(a)
  expect(screen.getByLabelText('已选文件')).toHaveTextContent('portrait.jpg')
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  expect(api.uploadAsset).toHaveBeenCalledWith(a, 'g1', 'Image', 7, expect.any(AbortSignal))
})

it('allows drag/drop and clears the selected file when the asset type changes', async () => {
  const user = userEvent.setup()
  render(<CreateAssetDialog {...props()} />)
  fireEvent.drop(screen.getByLabelText('文件拖放区'), { dataTransfer: { files: [photo()] } })
  expect(screen.getByText('portrait.jpg')).toBeVisible()
  await user.selectOptions(screen.getByLabelText('素材类型'), 'Audio')
  expect(screen.getByText('尚未选择文件')).toBeVisible()
  expect(screen.getByLabelText('本地素材文件')).toHaveAttribute('accept', '.mp3,.wav')
  expect(screen.getByRole('button', { name: '确认添加' })).toBeDisabled()
})

it.each(['format', 'size', 'empty'])('rejects invalid local files before upload: %s', async (kind) => {
  const file = kind === 'format' ? new File(['pdf'], 'x.pdf', { type: 'application/pdf' }) : photo()
  if (kind === 'size') Object.defineProperty(file, 'size', { value: 30 * 1024 * 1024 + 1 })
  if (kind === 'empty') Object.defineProperty(file, 'size', { value: 0 })
  render(<CreateAssetDialog {...props()} />)
  fireEvent.drop(screen.getByLabelText('文件拖放区'), { dataTransfer: { files: [file] } })
  expect(toast.error).toHaveBeenCalledOnce()
  expect(api.uploadAsset).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: '确认添加' })).toBeDisabled()
})

it('shows upload failure without calling create, and retries the upload', async () => {
  const user = userEvent.setup()
  vi.mocked(api.uploadAsset).mockRejectedValueOnce(new Error('空间不可用'))
  render(<CreateAssetDialog {...props()} />)
  await user.upload(screen.getByLabelText('本地素材文件'), photo())
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('第 1 步文件上传失败：空间不可用')
  expect(api.createAsset).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: '重试添加' }))
  await waitFor(() => expect(api.createAsset).toHaveBeenCalledOnce())
  expect(api.uploadAsset).toHaveBeenCalledTimes(2)
})

it('reuses an unexpired upload after create fails, but uploads again after expiry', async () => {
  const user = userEvent.setup()
  const uploaded = result()
  vi.mocked(api.uploadAsset).mockResolvedValue(uploaded)
  vi.mocked(api.createAsset).mockRejectedValue(new Error('入库被拒绝'))
  render(<CreateAssetDialog {...props()} />)
  await user.upload(screen.getByLabelText('本地素材文件'), photo())
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('第 2 步入库提交失败：入库被拒绝')
  await user.click(screen.getByRole('button', { name: '重试添加' }))
  expect(api.uploadAsset).toHaveBeenCalledOnce()
  expect(api.createAsset).toHaveBeenCalledTimes(2)
  vi.spyOn(Date, 'now').mockReturnValue(Number(uploaded.expiresAt) + 1)
  await user.click(screen.getByRole('button', { name: '重试添加' }))
  expect(api.uploadAsset).toHaveBeenCalledTimes(2)
  expect(toast.success).not.toHaveBeenCalled()
})

it.each(['cancel', 'escape', 'group', 'channel', 'unmount'])('aborts upload and ignores late upload results on %s', async (reason) => {
  const user = userEvent.setup()
  const upload = deferred<api.UploadedAsset>()
  vi.mocked(api.uploadAsset).mockReturnValue(upload.promise)
  const callbacks = props()
  const view = render(<CreateAssetDialog {...callbacks} />)
  await user.upload(screen.getByLabelText('本地素材文件'), photo())
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  const signal = vi.mocked(api.uploadAsset).mock.calls[0][4]
  if (!signal) throw new Error('upload must receive cancellation signal')
  if (reason === 'cancel') await user.click(screen.getByRole('button', { name: '取消' }))
  if (reason === 'escape') await user.keyboard('{Escape}')
  if (reason === 'group') view.rerender(<CreateAssetDialog {...callbacks} groupId="g2" groupName="新组" />)
  if (reason === 'channel') view.rerender(<CreateAssetDialog {...callbacks} channelId={8} />)
  if (reason === 'unmount') view.unmount()
  expect(signal.aborted).toBe(true)
  await act(async () => upload.resolve(result()))
  expect(api.createAsset).not.toHaveBeenCalled()
  expect(callbacks.onSuccess).not.toHaveBeenCalled()
  expect(toast.success).not.toHaveBeenCalled()
  if (reason === 'group') {
    expect(screen.getByText('尚未选择文件')).toBeVisible()
    await user.upload(screen.getByLabelText('本地素材文件'), photo())
    vi.mocked(api.uploadAsset).mockResolvedValue(result())
    await user.click(screen.getByRole('button', { name: '确认添加' }))
    await waitFor(() => expect(api.createAsset).toHaveBeenCalledWith(expect.objectContaining({ groupId: 'g2' }), 7, expect.any(AbortSignal)))
  }
})

it('aborts create on close and suppresses a late create error', async () => {
  const user = userEvent.setup()
  const create = deferred<unknown>()
  vi.mocked(api.createAsset).mockReturnValue(create.promise)
  const callbacks = props()
  render(<CreateAssetDialog {...callbacks} />)
  await user.upload(screen.getByLabelText('本地素材文件'), photo())
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  await waitFor(() => expect(api.createAsset).toHaveBeenCalledOnce())
  const signal = vi.mocked(api.createAsset).mock.calls[0][2]
  if (!signal) throw new Error('create must receive cancellation signal')
  await user.click(screen.getByRole('button', { name: '取消' }))
  expect(signal.aborted).toBe(true)
  await act(async () => create.reject(new Error('late error')))
  expect(callbacks.onSuccess).not.toHaveBeenCalled()
  expect(toast.error).not.toHaveBeenCalled()
})

it('keeps URL mode available without uploading a local file', async () => {
  const user = userEvent.setup()
  render(<CreateAssetDialog {...props()} />)
  await user.selectOptions(screen.getByLabelText('素材来源'), 'url')
  await user.type(screen.getByLabelText('素材名称'), '已有图片')
  await user.type(screen.getByLabelText('公网素材 URL'), 'https://example.com/photo.jpg')
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  expect(api.uploadAsset).not.toHaveBeenCalled()
  expect(api.createAsset).toHaveBeenCalledWith(expect.objectContaining({ assetUrl: 'https://example.com/photo.jpg' }), 7, expect.any(AbortSignal))
})

it.each([1, 2, 15, 16])('checks local audio duration %s seconds before upload and releases the object URL', async (duration) => {
  const user = userEvent.setup()
  const revoke = vi.fn()
  const OriginalURL = URL
  vi.stubGlobal('URL', class extends OriginalURL {
    static createObjectURL = vi.fn(() => 'blob:test-audio')
    static revokeObjectURL = revoke
  })
  const createElement = document.createElement.bind(document)
  vi.spyOn(document, 'createElement').mockImplementation((tag: string, options?: ElementCreationOptions) => {
    const element = createElement(tag, options)
    if (tag === 'audio') {
      Object.defineProperty(element, 'duration', { value: duration })
      Object.defineProperty(element, 'src', { set: () => queueMicrotask(() => element.dispatchEvent(new Event('loadedmetadata'))) })
    }
    return element
  })
  render(<CreateAssetDialog {...props()} />)
  await user.selectOptions(screen.getByLabelText('素材类型'), 'Audio')
  await user.upload(screen.getByLabelText('本地素材文件'), new File(['audio'], 'voice.mp3', { type: 'audio/mpeg' }))
  await user.click(screen.getByRole('button', { name: '确认添加' }))
  if (duration < 2 || duration > 15) {
    expect(await screen.findByRole('alert')).toHaveTextContent('2–15 秒')
    expect(api.uploadAsset).not.toHaveBeenCalled()
  } else {
    await waitFor(() => expect(api.uploadAsset).toHaveBeenCalledWith(expect.any(File), 'g1', 'Audio', 7, expect.any(AbortSignal)))
  }
  expect(revoke).toHaveBeenCalledWith('blob:test-audio')
})

it('aborts a timed out upload and reports its failed stage', async () => {
  vi.useFakeTimers()
  vi.mocked(api.uploadAsset).mockImplementation((_file, _group, _type, _channelId, signal) => new Promise((_resolve, reject) => {
    if (!signal) throw new Error('upload must receive cancellation signal')
    signal.addEventListener('abort', () => reject(new Error('canceled')))
  }))
  render(<CreateAssetDialog {...props()} />)
  fireEvent.change(screen.getByLabelText('本地素材文件'), { target: { files: [photo()] } })
  fireEvent.click(screen.getByRole('button', { name: '确认添加' }))
  await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })
  expect(screen.getByRole('alert')).toHaveTextContent('第 1 步文件上传失败：请求超时')
  expect(api.createAsset).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: '重试添加' })).toBeEnabled()
})
