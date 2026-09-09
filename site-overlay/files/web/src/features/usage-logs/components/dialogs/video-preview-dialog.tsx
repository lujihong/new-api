/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ExternalLink, Copy, Play, AlertCircle } from 'lucide-react'
import { useState, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

interface VideoPreviewDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  videoUrl: string
  taskId?: string
}

export function VideoPreviewDialog({
  open,
  onOpenChange,
  videoUrl,
  taskId,
}: VideoPreviewDialogProps) {
  const { t } = useTranslation()
  const [hasError, setHasError] = useState(false)
  const videoRef = useRef<HTMLVideoElement>(null)

  useEffect(() => {
    setHasError(false)
  }, [videoUrl, open])

  const handleCopy = () => {
    navigator.clipboard.writeText(videoUrl)
    toast.success(t('Copied'))
  }

  const handleOpenNewTab = () => {
    window.open(videoUrl, '_blank')
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Video Preview')}
      description={
        taskId ? `${t('Task ID:')} ${taskId}` : t('Preview generated video')
      }
      contentClassName='sm:max-w-3xl'
    >
      <div className='flex flex-col gap-4 py-2'>
        <div className='bg-muted/40 relative flex aspect-video w-full items-center justify-center overflow-hidden rounded-xl border'>
          {hasError ? (
            <div className='flex flex-col items-center gap-2 p-6 text-center'>
              <AlertCircle className='text-destructive size-8' />
              <p className='text-sm font-medium'>
                {t('Video playback failed')}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t('Please try opening in a new tab or check network connectivity')}
              </p>
              <Button
                variant='outline'
                size='sm'
                className='mt-2 gap-1.5'
                onClick={handleOpenNewTab}
              >
                <ExternalLink className='size-3.5' />
                {t('Open in new tab')}
              </Button>
            </div>
          ) : (
            <video
              ref={videoRef}
              src={videoUrl}
              controls
              autoPlay
              playsInline
              className='h-full w-full object-contain'
              onError={() => setHasError(true)}
            />
          )}
        </div>

        <div className='flex flex-wrap items-center justify-between gap-2 border-t pt-3'>
          <div className='text-muted-foreground font-mono text-xs truncate max-w-sm'>
            {taskId || ''}
          </div>
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              className='h-8 gap-1.5 text-xs'
              onClick={handleCopy}
            >
              <Copy className='size-3.5' />
              {t('Copy Link')}
            </Button>
            <Button
              variant='default'
              size='sm'
              className='h-8 gap-1.5 text-xs'
              onClick={handleOpenNewTab}
            >
              <ExternalLink className='size-3.5' />
              {t('Open in new tab')}
            </Button>
          </div>
        </div>
      </div>
    </Dialog>
  )
}
