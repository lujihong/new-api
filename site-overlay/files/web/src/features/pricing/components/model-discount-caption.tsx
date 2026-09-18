import { Info, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

import type { PricingModel } from '../types'

export interface ModelDiscountCaptionProps {
  model: PricingModel
  /**
   * 展现模式：
   * - 'card'：模型卡片上展示（默认；无折扣隐藏，有折扣显示高亮专属徽标）
   * - 'table'：表格列表展示（无折扣隐藏，有折扣显示紧凑优惠标识）
   * - 'details'：详情抽屉中展示（直观卡片，有折扣详细说明扣费规则，无折扣说明沿用组价）
   * - 'caption'：行内纯文本模式（兼容已有场景）
   */
  variant?: 'card' | 'table' | 'details' | 'caption'
  /** 是否在原价（factor === 1）时也强制显示（默认 false） */
  showFullPrice?: boolean
  className?: string
}

export function ModelDiscountCaption({
  model,
  variant = 'card',
  showFullPrice = false,
  className,
}: ModelDiscountCaptionProps) {
  const { t } = useTranslation()
  const discount = model.model_discount

  if (!discount || discount.model !== model.model_name) return null

  const factor = discount.factor
  const hasDiscount = typeof factor === 'number' && factor < 1 && factor >= 0

  // 没有额外折扣时（factor === 1 或默认）：卡片、表格与默认行内模式直接不显示提示，保持界面清爽
  if (!hasDiscount && !showFullPrice && variant !== 'details') {
    return null
  }

  // 折扣来源名称
  let sourceTitle = t('Model discount', { defaultValue: '专属优惠' })
  if (discount.source === 'user') {
    sourceTitle = t('Personal discount', { defaultValue: '个人专属' })
  } else if (discount.source === 'group') {
    sourceTitle = t('Customer group discount', { defaultValue: '客户组优惠' })
  }

  // 折扣折数文本
  let discountLabel = t('Standard group price', { defaultValue: '沿用组价' })
  if (factor === 0) {
    discountLabel = t('Free model discount', { defaultValue: '免费' })
  } else if (factor !== 1) {
    discountLabel = t('Discount tenths', {
      defaultValue: '{{value}}折',
      value: Number((factor * 10).toFixed(4)),
    })
  }

  // 1. 详情抽屉场景：展示直观清晰的人性化说明卡片
  if (variant === 'details') {
    if (hasDiscount) {
      return (
        <div
          className={cn(
            'flex items-start gap-2.5 rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-xs text-emerald-800 dark:border-emerald-500/20 dark:bg-emerald-950/30 dark:text-emerald-300',
            className
          )}
        >
          <Sparkles className='mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400' />
          <div className='min-w-0 flex-1 space-y-0.5'>
            <div className='flex flex-wrap items-center gap-1.5 font-bold'>
              <span>已享{sourceTitle}：{discountLabel}</span>
              <span className='font-mono font-normal opacity-85'>（实付系数 ×{factor}）</span>
            </div>
            <p className='text-[11px] leading-relaxed text-emerald-700/90 dark:text-emerald-400/90'>
              {t('Model discount details note', {
                defaultValue:
                  '您调用此模型时，结算扣费将在当前账号分组基础倍率上额外享受此专属折扣。',
              })}
            </p>
          </div>
        </div>
      )
    }

    // 无额外折扣时在详情中的直观提示
    return (
      <div
        className={cn(
          'flex items-center gap-2 rounded-lg border border-stone-200/70 bg-stone-50/50 px-3 py-2 text-xs text-stone-600 dark:border-stone-800 dark:bg-stone-900/40 dark:text-stone-400',
          className
        )}
      >
        <Info className='size-3.5 shrink-0 text-stone-400' />
        <span>
          {t('Standard group price applied', {
            defaultValue: '当前模型执行账号分组标准组价，未设置额外单模型折扣。',
          })}
        </span>
      </div>
    )
  }

  // 2. 卡片场景：仅当真正有折扣时，渲染高质感、醒目的优惠 Badge
  if (variant === 'card') {
    return (
      <div className={cn('flex items-center gap-1.5', className)}>
        <Badge
          variant='secondary'
          className={cn(
            'gap-1 px-1.5 py-0.5 text-[11px] font-semibold tracking-tight shadow-2xs',
            discount.source === 'user'
              ? 'border-amber-500/35 bg-amber-500/10 text-amber-700 dark:border-amber-500/30 dark:bg-amber-950/40 dark:text-amber-300'
              : 'border-emerald-500/35 bg-emerald-500/10 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-950/40 dark:text-emerald-300'
          )}
        >
          <Sparkles className='size-2.5 shrink-0' />
          <span>{sourceTitle} · {discountLabel}</span>
          <span className='font-mono font-normal opacity-80'>（×{factor}）</span>
        </Badge>
      </div>
    )
  }

  // 3. 表格列表场景
  if (variant === 'table') {
    return (
      <div className={cn('inline-flex items-center gap-1', className)}>
        <Badge
          variant='outline'
          className={cn(
            'h-5 px-1.5 text-[10px] font-medium',
            discount.source === 'user'
              ? 'border-amber-500/30 text-amber-600 dark:text-amber-400'
              : 'border-emerald-500/30 text-emerald-600 dark:text-emerald-400'
          )}
        >
          {sourceTitle} · {discountLabel}
        </Badge>
      </div>
    )
  }

  // 4. 行内文本场景（兼容模式）
  return (
    <span className={cn('text-primary block text-xs', className)}>
      {sourceTitle} · {discountLabel} · ×{factor}
    </span>
  )
}
