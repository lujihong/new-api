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
import { Shield01Icon, Wrench01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Label } from '@/components/ui/label'
import { formatLogQuota, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import { taskActionMapper, taskStatusMapper } from '../../lib/mappers'
import { resolveTaskDetailAccess } from '../../lib/task-details'
import type { TaskLog } from '../../types'
import { PluginAuthorLink } from '../plugin-author-link'

function DetailRow(props: {
  label: React.ReactNode
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className='grid min-w-0 grid-cols-[6rem_minmax(0,1fr)] gap-2 text-sm sm:grid-cols-[8rem_minmax(0,1fr)]'>
      <span className='text-muted-foreground text-xs'>{props.label}</span>
      <span
        className={cn(
          'min-w-0 text-xs break-all sm:wrap-break-word',
          props.mono && 'font-mono'
        )}
      >
        {props.value}
      </span>
    </div>
  )
}

function DetailSection(props: {
  label: string
  icon?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className='min-w-0 space-y-1.5'>
      <Label className='flex items-center gap-1.5 text-xs font-semibold'>
        {props.icon}
        {props.label}
      </Label>
      <div className='bg-muted/30 min-w-0 space-y-1.5 rounded-md border p-2.5'>
        {props.children}
      </div>
    </section>
  )
}

function formatTaskTimestamp(value?: number): string {
  return value ? formatTimestampToDate(value, 'seconds') : '-'
}

interface TaskDetailsDialogProps {
  log: TaskLog
  isAdmin: boolean
  isRoot: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function TaskDetailsDialog(props: TaskDetailsDialogProps) {
  const { t } = useTranslation()
  const access = resolveTaskDetailAccess(props.log, props.isAdmin, props.isRoot)
  const plugin = access.plugin
  const runtime = access.runtime
  const properties = props.log.properties

  const rawData =
    typeof props.log.data === 'object' && props.log.data !== null
      ? (props.log.data as Record<string, any>)
      : undefined
  const resultObj =
    rawData?.result && typeof rawData.result === 'object'
      ? rawData.result
      : undefined
  const usageObj = resultObj?.usage || rawData?.usage
  const completionTokens =
    typeof usageObj?.completion_tokens === 'number'
      ? usageObj.completion_tokens
      : undefined
  const totalTokens =
    typeof usageObj?.total_tokens === 'number' ? usageObj.total_tokens : undefined
  const resResolution = resultObj?.resolution || rawData?.resolution
  const resDuration = resultObj?.duration || rawData?.duration
  const resRatio = resultObj?.ratio || rawData?.ratio

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <span className='flex items-center gap-2'>
          {t('Task Details')}
          <StatusBadge
            label={t(
              taskStatusMapper.getLabel(
                props.log.status,
                props.log.status || 'Submitting'
              )
            )}
            variant={taskStatusMapper.getVariant(props.log.status)}
            size='sm'
            copyable={false}
          />
        </span>
      }
      description={t('View the complete details for this task')}
      contentClassName='min-w-0 overflow-hidden sm:max-w-2xl'
      contentHeight='min(72dvh, 720px)'
      bodyClassName='pr-2 sm:pr-4'
    >
      <div className='space-y-3'>
        <DetailSection label={t('Basic Information')}>
          <DetailRow label={t('Task ID')} value={props.log.task_id} mono />
          <DetailRow label={t('Platform')} value={props.log.platform} mono />
          <DetailRow
            label={t('Action')}
            value={t(taskActionMapper.getLabel(props.log.action))}
          />
          <DetailRow
            label={t('Progress')}
            value={props.log.progress || '-'}
            mono
          />
          <DetailRow
            label={t('Submit Time')}
            value={formatTaskTimestamp(props.log.submit_time)}
            mono
          />
          <DetailRow
            label={t('Start Time')}
            value={formatTaskTimestamp(props.log.start_time)}
            mono
          />
          <DetailRow
            label={t('Finish Time')}
            value={formatTaskTimestamp(props.log.finish_time)}
            mono
          />
          {properties?.origin_model_name ? (
            <DetailRow
              label={t('Original Model')}
              value={properties.origin_model_name}
              mono
            />
          ) : null}
          {properties?.upstream_model_name ? (
            <DetailRow
              label={t('Actual Model')}
              value={properties.upstream_model_name}
              mono
            />
          ) : null}
          {props.log.fail_reason ? (
            <DetailRow label={t('Fail Reason')} value={props.log.fail_reason} />
          ) : null}
        </DetailSection>

        {(completionTokens != null || totalTokens != null || resResolution || resDuration || resRatio) ? (
          <DetailSection label={t('Usage & Output', { defaultValue: '用量与生成参数' })}>
            {resResolution ? (
              <DetailRow label={t('Resolution', { defaultValue: '分辨率' })} value={String(resResolution)} mono />
            ) : null}
            {resRatio ? (
              <DetailRow label={t('Aspect Ratio', { defaultValue: '画面比例' })} value={String(resRatio)} mono />
            ) : null}
            {resDuration != null ? (
              <DetailRow label={t('Video Duration', { defaultValue: '视频时长' })} value={`${resDuration}s`} mono />
            ) : null}
            {completionTokens != null ? (
              <DetailRow
                label={t('Completion Tokens', { defaultValue: '生成 Tokens' })}
                value={completionTokens.toLocaleString()}
                mono
              />
            ) : null}
            {totalTokens != null && totalTokens !== completionTokens ? (
              <DetailRow
                label={t('Total Tokens', { defaultValue: '总计 Tokens' })}
                value={totalTokens.toLocaleString()}
                mono
              />
            ) : null}
            <DetailRow
              label={t('Actual Quota', { defaultValue: '最终扣除额度' })}
              value={formatLogQuota(props.log.quota)}
              mono
            />
          </DetailSection>
        ) : null}

        {props.isAdmin ? (
          <DetailSection
            label={t('Admin Only')}
            icon={
              <HugeiconsIcon
                icon={Shield01Icon}
                className='size-3.5 text-blue-500'
                strokeWidth={2}
              />
            }
          >
            <DetailRow
              label={t('User')}
              value={props.log.username || String(props.log.user_id)}
            />
            <DetailRow
              label={t('Channel')}
              value={`#${props.log.channel_id}`}
              mono
            />
            <DetailRow label={t('Group')} value={props.log.group || '-'} />
            <DetailRow
              label={t('Quota')}
              value={formatLogQuota(props.log.quota)}
              mono
            />
            {props.log.admin_info?.request_id ? (
              <DetailRow
                label={t('Request ID')}
                value={props.log.admin_info.request_id}
                mono
              />
            ) : null}
            {props.log.admin_info?.request_path ? (
              <DetailRow
                label={t('Request Path')}
                value={props.log.admin_info.request_path}
                mono
              />
            ) : null}
            {plugin ? (
              <>
                <DetailRow
                  label={t('Task Plugin')}
                  value={plugin.name || plugin.key}
                />
                <DetailRow label={t('Plugin key')} value={plugin.key} mono />
                <DetailRow
                  label={t('Version')}
                  value={plugin.version || '-'}
                  mono
                />
                {plugin.author ? (
                  <DetailRow
                    label={t('Plugin author')}
                    value={<PluginAuthorLink author={plugin.author} showUrl />}
                  />
                ) : null}
              </>
            ) : null}
          </DetailSection>
        ) : null}

        {props.isRoot && props.log.root_info ? (
          <DetailSection
            label={t('Root Diagnostics')}
            icon={
              <HugeiconsIcon
                icon={Wrench01Icon}
                className='size-3.5 text-amber-500'
                strokeWidth={2}
              />
            }
          >
            {runtime ? (
              <>
                <DetailRow
                  label={t('API Version')}
                  value={String(runtime.api_version)}
                  mono
                />
                <DetailRow
                  label={t('Plugin Generation')}
                  value={String(runtime.generation)}
                  mono
                />
              </>
            ) : null}
            {access.upstreamTaskId ? (
              <DetailRow
                label={t('Upstream Task ID')}
                value={access.upstreamTaskId}
                mono
              />
            ) : null}
            {access.nodeName ? (
              <DetailRow label={t('Node Name')} value={access.nodeName} mono />
            ) : null}
          </DetailSection>
        ) : null}
      </div>
    </Dialog>
  )
}
