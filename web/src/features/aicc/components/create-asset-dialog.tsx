import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { createAsset, uploadAsset, uploadExpiry, type UploadedAsset } from '../api'
import type { Asset } from '../types'

interface CreateAssetDialogProps {
  open: boolean
  groupId: string
  groupName: string
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

const limits = {
  Image: { bytes: 30 * 1024 * 1024, extensions: /\.(jpe?g|png|webp)$/i, mimes: ['image/jpeg', 'image/png', 'image/webp'], accept: '.jpg,.jpeg,.png,.webp', label: 'JPEG / PNG / WebP，最大 30 MiB' },
  Video: { bytes: 50 * 1024 * 1024, extensions: /\.(mp4|mov)$/i, mimes: ['video/mp4', 'video/quicktime'], accept: '.mp4,.mov', label: 'MP4 / MOV，最大 50 MiB' },
  Audio: { bytes: 15 * 1024 * 1024, extensions: /\.(mp3|wav)$/i, mimes: ['audio/mpeg', 'audio/mp3', 'audio/wav', 'audio/x-wav', 'audio/wave'], accept: '.mp3,.wav', label: 'MP3 / WAV，最大 15 MiB' },
}

// Local metadata is only an early check; the gateway/platform still validates the file.
function validateDuration(file: File, type: 'Video' | 'Audio', signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const media = document.createElement(type === 'Video' ? 'video' : 'audio')
    const url = URL.createObjectURL(file)
    const finish = (error?: Error) => {
      clearTimeout(timer)
      signal.removeEventListener('abort', abort)
      media.removeEventListener('loadedmetadata', loaded)
      media.removeEventListener('error', failed)
      media.removeAttribute('src')
      URL.revokeObjectURL(url)
      if (error) reject(error)
      else resolve()
    }
    const abort = () => finish(new Error('已取消'))
    const timer = setTimeout(() => finish(new Error('读取音视频时长超时，请重试')), 10_000)
    media.preload = 'metadata'
    const loaded = () => finish(Number.isFinite(media.duration) && media.duration >= 2 && media.duration <= 15
      ? undefined : new Error('本平台保守限制：音视频须为 2–15 秒'))
    const failed = () => finish(new Error('无法读取音视频时长，请换用浏览器可解析的文件'))
    media.addEventListener('loadedmetadata', loaded)
    media.addEventListener('error', failed)
    signal.addEventListener('abort', abort, { once: true })
    if (signal.aborted) abort()
    else media.src = url
  })
}

export function CreateAssetDialog(props: CreateAssetDialogProps) {
  return <CreateAssetDialogContent key={`${props.groupId}:${props.open}`} {...props} />
}

function CreateAssetDialogContent({ open, groupId, groupName, onOpenChange, onSuccess }: CreateAssetDialogProps) {
  const [stage, setStage] = useState<'idle' | 'uploading' | 'creating'>('idle')
  const [mode, setMode] = useState<'file' | 'url'>('file')
  const [assetName, setAssetName] = useState('')
  const [assetUrl, setAssetUrl] = useState('')
  const [assetType, setAssetType] = useState<Asset['assetType']>('Image')
  const [file, setFile] = useState<File | null>(null)
  const [uploaded, setUploaded] = useState<UploadedAsset | null>(null)
  const [submitted, setSubmitted] = useState(false)
  const [failure, setFailure] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const request = useRef<AbortController | null>(null)
  const generation = useRef(0)
  const loading = stage !== 'idle'

  const invalidate = () => {
    generation.current++
    request.current?.abort()
    request.current = null
  }
  useEffect(() => () => {
    generation.current++
    request.current?.abort()
  }, [])

  const close = (next: boolean) => {
    if (!next) invalidate()
    onOpenChange(next)
  }
  const clearFile = () => {
    setFile(null)
    setUploaded(null)
    setSubmitted(false)
    setFailure('')
    if (input.current) input.current.value = ''
  }
  const chooseFile = (next?: File) => {
    if (loading || !next) return
    const rule = limits[assetType]
    if (!rule.extensions.test(next.name) || (next.type && !rule.mimes.includes(next.type))) {
      clearFile()
      setFailure(`文件格式不符：${rule.label}`)
      toast.error(`文件格式不符：${rule.label}`)
      return
    }
    if (!next.size || next.size > rule.bytes) {
      clearFile()
      setFailure(`文件为空或超出大小限制：${rule.label}`)
      toast.error(`文件为空或超出大小限制：${rule.label}`)
      return
    }
    setUploaded(null)
    setSubmitted(false)
    setFailure('')
    setFile(next)
    setAssetName(next.name.replace(/\.[^.]+$/, '').slice(0, 64))
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (request.current || !open || submitted) return
    if (!assetName.trim()) { toast.error('请输入素材名称'); return }
    if (mode === 'file' && !file) { toast.error('请选择本地文件'); return }
    if (mode === 'url' && !/^https?:\/\//i.test(assetUrl.trim())) {
      toast.error('请输入有效的公网可访问 HTTP/HTTPS 素材链接')
      return
    }
    const controller = new AbortController()
    request.current = controller
    const current = ++generation.current
    const isCurrent = () => current === generation.current && !controller.signal.aborted
    let phase: 'upload' | 'create' = mode === 'file' ? 'upload' : 'create'
    let timedOut = false
    let timeout: ReturnType<typeof setTimeout> | undefined
    const armTimeout = (ms: number) => {
      clearTimeout(timeout)
      timeout = setTimeout(() => { timedOut = true; controller.abort() }, ms)
    }
    setFailure('')
    try {
      let url = assetUrl.trim()
      if (mode === 'file' && file) {
        setStage('uploading')
        armTimeout(120_000)
        if (uploaded && uploadExpiry(uploaded.expiresAt) > Date.now()) {
          url = uploaded.url
        } else {
          setUploaded(null)
          if (assetType !== 'Image') await validateDuration(file, assetType, controller.signal)
          if (!isCurrent()) return
          const result = await uploadAsset(file, groupId, assetType, controller.signal)
          if (!isCurrent()) return
          setUploaded(result)
          url = result.url
        }
      }
      if (!isCurrent()) return
      phase = 'create'
      setStage('creating')
      armTimeout(60_000)
      await createAsset({ groupId, assetName: assetName.trim(), assetUrl: url, assetType }, controller.signal)
      if (!isCurrent()) return
      setSubmitted(true)
      toast.success('入库任务已提交，仍需平台异步校验；请刷新查看状态，并非已可用')
      onSuccess?.()
      close(false)
    } catch (error) {
      if (current !== generation.current || (controller.signal.aborted && !timedOut)) return
      let detail = '网络异常'
      if (error instanceof Error) detail = error.message
      if (timedOut) detail = '请求超时'
      const message = phase === 'upload' ? `第 1 步文件上传失败：${detail}`
        : `第 2 步入库提交失败：${detail}。如请求超时，可能已受理，请先刷新素材列表核对，避免重复入库。`
      setFailure(message)
      toast.error(message)
    } finally {
      clearTimeout(timeout)
      if (current === generation.current) {
        request.current = null
        setStage('idle')
      }
    }
  }

  let fileStatus = '已选择 · 尚未上传'
  if (uploaded) fileStatus = '已上传 · 待重试入库提交'
  if (submitted) fileStatus = '已提交 · 等待平台校验'
  if (stage === 'uploading') fileStatus = '正在校验并上传'
  if (stage === 'creating') fileStatus = '已上传 · 正在提交入库'
  let submitLabel = '确认添加'
  if (failure && (mode === 'url' || file)) submitLabel = '重试添加'
  if (stage === 'uploading') submitLabel = '第 1 步：校验并上传文件…'
  if (stage === 'creating') submitLabel = '第 2 步：提交入库…'

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-md">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>添加新素材</DialogTitle>
            <DialogDescription>目标素材组：<span className="font-semibold text-foreground">{groupName}</span>（本人素材）</DialogDescription>
          </DialogHeader>
          <fieldset disabled={loading} className="space-y-4 py-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium">素材来源</label>
              <select aria-label="素材来源" value={mode} onChange={(e) => { setMode(e.target.value as 'file' | 'url'); clearFile(); setAssetUrl('') }} className="w-full rounded border bg-background p-2">
                <option value="file">本地文件（推荐）</option>
                <option value="url">公网 URL</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium">素材类型</label>
              <select aria-label="素材类型" value={assetType} onChange={(e) => { setAssetType(e.target.value as Asset['assetType']); clearFile(); setAssetUrl(''); setAssetName('') }} className="w-full rounded border bg-background p-2">
                <option value="Image">图片 (Image)</option>
                <option value="Video">视频 (Video)</option>
                <option value="Audio">音频 (Audio)</option>
              </select>
            </div>
            {mode === 'file' ? (
              <div aria-label="文件拖放区" className="space-y-2 rounded border-2 border-dashed p-4"
                onDragOver={(e) => e.preventDefault()} onDrop={(e) => { e.preventDefault(); if (loading || !e.dataTransfer.files[0]) return; if (input.current) input.current.value = ''; chooseFile(e.dataTransfer.files[0]) }}>
                <input ref={input} className="hidden" type="file" aria-label="本地素材文件" accept={limits[assetType].accept} onChange={(e) => chooseFile(e.target.files?.[0])} />
                <div className="flex flex-wrap items-center gap-3">
                  <Button type="button" variant="outline" onClick={() => input.current?.click()}>{file ? '更换文件' : '选择文件'}</Button>
                  {file && <Button type="button" variant="ghost" onClick={clearFile}>移除文件</Button>}
                  <span className="text-xs text-muted-foreground">也可拖放一个文件到此处</span>
                </div>
                {file ? <div aria-label="已选文件" className="rounded-md border bg-muted/40 p-3">
                  <p className="text-xs font-medium text-foreground">{fileStatus}</p>
                  <p className="mt-1 break-all text-sm font-medium">{file.name}</p>
                  <p className="mt-1 text-xs text-muted-foreground">{file.size < 1024 * 1024 ? `${Math.max(1, Math.ceil(file.size / 1024))} KB` : `${(file.size / (1024 * 1024)).toFixed(1)} MiB`}</p>
                </div> : <p className="text-sm text-muted-foreground">尚未选择文件</p>}
                <p className="text-xs text-muted-foreground">{limits[assetType].label}</p>
              </div>
            ) : (
              <div className="space-y-1.5">
                <label className="text-sm font-medium">公网可访问文件链接 (URL) *</label>
                <Input aria-label="公网素材 URL" placeholder="https://your-domain.com/path/to/image.png" value={assetUrl} onChange={(e) => setAssetUrl(e.target.value)} required />
                <p className="text-xs text-muted-foreground">平台异步下载并校验链接内容；URL 模式跳过本地上传。</p>
              </div>
            )}
            <div className="space-y-1.5">
              <label className="text-sm font-medium">素材名称 *</label>
              <Input aria-label="素材名称" placeholder="例如：主讲人正面形象照" value={assetName} onChange={(e) => setAssetName(e.target.value)} maxLength={64} required />
            </div>
            <p className="text-xs text-muted-foreground">音视频采用 2–15 秒的保守平台限制，并非官方完整协议。同一人的新照片每次仍需一致性校验；不保证任意人或任意图片可入库。请确保具备肖像及素材授权。</p>
          </fieldset>
          {uploaded && <p className="mb-3 text-xs" role="status">文件已上传，未过期链接可复用于入库重试，无需重复上传；过期后将重新上传。</p>}
          {failure && <p role="alert" className="mb-3 text-sm text-destructive">{failure}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => close(false)}>取消</Button>
            <Button type="submit" disabled={loading || submitted || !assetName.trim() || (mode === 'file' ? !file : !assetUrl.trim())}>
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
