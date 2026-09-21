import { useState, useEffect, useRef, useCallback } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import { Copy, Check, RefreshCw, ExternalLink, ShieldCheck, AlertCircle } from 'lucide-react'
import { toast } from 'sonner'

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { createH5Session, queryGroupByBytedToken } from '../api'
import type { H5SessionResponse } from '../types'

interface RealPersonAuthDialogProps {
  channelId: number
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

export function RealPersonAuthDialog(props: RealPersonAuthDialogProps) {
  return <RealPersonAuthDialogContent key={`${props.channelId}:${props.open}`} {...props} />
}

function RealPersonAuthDialogContent({
  channelId,
  open,
  onOpenChange,
  onSuccess,
}: RealPersonAuthDialogProps) {
  const [loading, setLoading] = useState(false)
  const [checking, setChecking] = useState(false)
  const [session, setSession] = useState<H5SessionResponse | null>(null)
  const [copied, setCopied] = useState(false)
  const [authSuccess, setAuthSuccess] = useState(false)
  const [expiresAt, setExpiresAt] = useState(0)
  const [clock, setClock] = useState(Date.now)
  const secondsLeft = Math.max(0, Math.ceil((expiresAt - clock) / 1000))
  useEffect(() => {
    if (!open || !session) return
    const interval = setInterval(() => setClock(Date.now()), 1000)
    return () => clearInterval(interval)
  }, [open, session])

  const request = useRef<AbortController | null>(null)
  const generation = useRef(0)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const initSession = useCallback(async () => {
    request.current?.abort()
    clearTimeout(timer.current)
    const controller = new AbortController()
    request.current = controller
    const current = ++generation.current
    setLoading(true)
    setChecking(false)
    setCopied(false)
    setSession(null)
    setAuthSuccess(false)
    try {
      const data = await createH5Session(channelId, controller.signal)
      if (!controller.signal.aborted && current === generation.current) {
        setSession(data)
        setClock(Date.now())
        setExpiresAt(Date.now() + data.expiresIn * 1000)
      }
    } catch (error) {
      if (!controller.signal.aborted && current === generation.current) toast.error(`创建认证会话失败：${error instanceof Error ? error.message : '网络异常'}`)
    } finally {
      if (!controller.signal.aborted && current === generation.current) setLoading(false)
    }
  }, [channelId])

  useEffect(() => {
    let active = true
    const lifetime = generation
    if (open) void Promise.resolve().then(() => { if (active) void initSession() })
    return () => {
      active = false
      lifetime.current++
      request.current?.abort()
      clearTimeout(timer.current)
    }
  }, [open, initSession])

  const copyLink = async () => {
    if (!session?.h5Link) return
    const current = generation.current
    try {
      await navigator.clipboard.writeText(session.h5Link)
      if (current !== generation.current) return
      setCopied(true)
      toast.success('H5 认证链接已复制到剪贴板')
      clearTimeout(timer.current)
      timer.current = setTimeout(() => { if (current === generation.current) setCopied(false) }, 2000)
    } catch {
      if (current === generation.current) toast.error('复制失败，请检查剪贴板权限后重试')
    }
  }

  const checkResult = async () => {
    if (!session?.bytedToken) return
    const current = generation.current
    const signal = request.current?.signal
    try {
      setChecking(true)
      const res = await queryGroupByBytedToken(session.bytedToken, signal)
      if (current !== generation.current || signal?.aborted) return
      if (res.authenticated === true) {
        setAuthSuccess(true)
        toast.success('真人肖像实名认证成功！素材组已关联上线')
        onSuccess?.()
      } else {
        toast.info('尚未查询到认证通过记录，请在手机端完成活体人脸核验后重试')
      }
    } catch (error) {
      if (current === generation.current && !signal?.aborted) toast.error(`查询认证失败：${error instanceof Error ? error.message : '网络异常'}`)
    } finally {
      if (current === generation.current && !signal?.aborted) setChecking(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <div className="flex items-center gap-2">
            <ShieldCheck className="w-5 h-5 text-emerald-500" />
            <DialogTitle>移动云 AICC · 真人实名肖像认证</DialogTitle>
          </div>
          <DialogDescription>
            国家深度合成合规要求：真人出镜必须本人完成活体人脸核验并授权
          </DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="flex flex-col items-center justify-center py-12 gap-3">
            <RefreshCw className="w-8 h-8 animate-spin text-muted-foreground" />
            <p className="text-sm text-muted-foreground">正在向移动云安全网关申请授权会话...</p>
          </div>
        )}
        {!loading && session?.h5Link && (
          <div className="flex flex-col items-center gap-4 py-2">
            {authSuccess ? (
              <div className="flex flex-col items-center justify-center py-8 text-center gap-2">
                <div className="w-12 h-12 rounded-full bg-emerald-100 dark:bg-emerald-950 flex items-center justify-center text-emerald-600">
                  <Check className="w-6 h-6" />
                </div>
                <h4 className="font-semibold text-lg text-emerald-600">实名核验与肖像授权已生效！</h4>
                <p className="text-sm text-muted-foreground max-w-xs">
                  真人素材组已关联到本人账号。同一人的新照片每次仍需一致性校验，素材状态为可用后方可用于生成；不保证任意人或任意图片可入库。
                </p>
              </div>
            ) : (
              <>
                <div className="p-3 bg-white rounded-xl shadow-sm border border-slate-200">
                  <QRCodeSVG value={session.h5Link} size={180} level="M" />
                </div>

                <div className="text-center space-y-1">
                  <p className="text-sm font-medium text-foreground">
                    请使用手机（微信或浏览器）扫描上方二维码
                  </p>
                  <p className="text-xs text-muted-foreground">
                    按屏幕指引录制面部活体检测并签署肖像授权书
                  </p>
                </div>

                <div className="flex w-full min-w-0 flex-wrap items-center gap-2">
                  <input
                    type="text"
                    aria-label="本人认证链接"
                    readOnly
                    value={session.h5Link}
                    className="min-w-0 flex-1 basis-full truncate rounded border bg-muted px-3 py-1.5 text-xs"
                  />
                  <Button variant="outline" size="sm" onClick={copyLink} className="shrink-0 gap-1">
                    {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                    复制链接
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => window.open(session.h5Link, '_blank')}
                    className="shrink-0 gap-1"
                  >
                    <ExternalLink className="w-3.5 h-3.5" />
                    打开
                  </Button>
                </div>

                <div className="flex items-start gap-2 rounded-md border bg-muted p-2.5 text-xs text-muted-foreground">
                  <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
                  <span>
                    {secondsLeft > 0 ? `认证链接剩余 ${Math.floor(secondsLeft / 60)}分${secondsLeft % 60}秒。手机完成后查询认证结果。` : '认证链接已过期，请刷新二维码重新发起。'}
                  </span>
                </div>
              </>
            )}
          </div>
        )}
        {!loading && !session?.h5Link && (
          <div className="py-8 text-center text-sm text-destructive">
            未能成功创建会话，请检查网络后点击重试
          </div>
        )}

        <DialogFooter className="flex gap-2 sm:justify-between">
          <Button variant="ghost" size="sm" onClick={initSession} disabled={loading}>
            <RefreshCw className={`w-3.5 h-3.5 mr-1 ${loading ? 'animate-spin' : ''}`} />
            刷新二维码
          </Button>

          {authSuccess ? (
            <Button onClick={() => onOpenChange(false)}>完成</Button>
          ) : (
            <Button onClick={checkResult} disabled={checking || !session || secondsLeft === 0}>
              {checking && <RefreshCw className="w-3.5 h-3.5 mr-1 animate-spin" />}
              我已在手机完成认证
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
