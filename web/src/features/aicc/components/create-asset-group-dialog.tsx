import React, { useEffect, useRef, useState } from 'react'
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
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { createAssetGroup } from '../api'

interface CreateAssetGroupDialogProps {
  channelId: number
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

export function CreateAssetGroupDialog(props: CreateAssetGroupDialogProps) {
  return <CreateAssetGroupDialogContent key={`${props.channelId}:${props.open}`} {...props} />
}

function CreateAssetGroupDialogContent({
  channelId,
  open,
  onOpenChange,
  onSuccess,
}: CreateAssetGroupDialogProps) {
  const [loading, setLoading] = useState(false)
  const [groupName, setGroupName] = useState('')
  const [description, setDescription] = useState('')
  const generation = useRef(0)
  const request = useRef<AbortController | null>(null)
  useEffect(() => {
    generation.current++
    const lifetime = generation
    return () => { lifetime.current++; request.current?.abort(); request.current = null }
  }, [open, channelId])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!open || request.current) return
    if (!groupName.trim()) {
      toast.error('请输入素材组名称')
      return
    }

    const current = generation.current
    const controller = new AbortController()
    request.current = controller
    try {
      setLoading(true)
      await createAssetGroup({
        groupName: groupName.trim(),
        description: description.trim(),
      }, channelId, controller.signal)
      if (current !== generation.current) return
      toast.success('素材组创建成功')
      setGroupName('')
      setDescription('')
      onOpenChange(false)
      onSuccess?.()
    } catch (error) {
      if (current === generation.current) toast.error(`创建素材组失败：${error instanceof Error ? error.message : '网络异常'}`)
    } finally {
      if (current === generation.current) { request.current = null; setLoading(false) }
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>新建虚拟人像素材组 (AIGC)</DialogTitle>
            <DialogDescription>
              创建一个素材分组，用于收纳虚拟数字人形象、背景图或道具素材
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium">素材组名称 *</label>
              <Input
                aria-label="素材组名称"
                placeholder="例如：商务数字人形象"
                value={groupName}
                onChange={(e) => setGroupName(e.target.value)}
                maxLength={64}
                required
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-sm font-medium">描述说明（选填）</label>
              <Textarea
                aria-label="描述说明"
                placeholder="例如：专用于营销发售口播的虚拟西装形象"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                maxLength={300}
                rows={3}
              />
            </div>
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={loading}
            >
              取消
            </Button>
            <Button type="submit" disabled={loading || !groupName.trim()}>
              {loading ? '创建中...' : '确认创建'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
