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
import { Code2, Eye, HelpCircle } from 'lucide-react'
import { memo, useCallback, useMemo, useState, type ReactNode } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { JsonCodeEditor } from '@/components/json-code-editor'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageActionsPortal } from '../components/settings-page-context'
import { safeJsonParse } from '../utils/json-parser'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { GroupRatioVisualEditor } from './group-ratio-visual-editor'
import { GroupSpecialUsableRulesEditor } from './group-special-usable-editor'

type GroupFormValues = {
  GroupRatio: string
  TopupGroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  MaxTokenAutoGroups: number
  DefaultUseAutoGroup: boolean
  GroupSpecialUsableGroup: string
}

type GroupRatioFormProps = {
  form: UseFormReturn<GroupFormValues>
  onSave: (values: GroupFormValues) => Promise<void>
  isSaving: boolean
}

export const GroupRatioForm = memo(function GroupRatioForm({
  form,
  onSave,
  isSaving,
}: GroupRatioFormProps) {
  const { t } = useTranslation()
  const [editMode, setEditMode] = useState<'visual' | 'json'>('visual')
  const [guideOpen, setGuideOpen] = useState(false)

  const handleFieldChange = useCallback(
    (field: keyof GroupFormValues, value: string) => {
      form.setValue(field, value, {
        shouldValidate: true,
        shouldDirty: true,
      })
    },
    [form]
  )

  const toggleEditMode = useCallback(() => {
    setEditMode((prev) => (prev === 'visual' ? 'json' : 'visual'))
  }, [])

  const watchedGroupRatio = form.watch('GroupRatio')
  const watchedUserUsableGroups = form.watch('UserUsableGroups')
  const watchedTopupGroupRatio = form.watch('TopupGroupRatio')
  const groupNames = useMemo(() => {
    const ratioMap = safeJsonParse<Record<string, number>>(watchedGroupRatio, {
      fallback: {},
      silent: true,
    })
    const usableMap = safeJsonParse<Record<string, string>>(
      watchedUserUsableGroups,
      { fallback: {}, silent: true }
    )
    const topupMap = safeJsonParse<Record<string, number>>(
      watchedTopupGroupRatio,
      { fallback: {}, silent: true }
    )
    return [
      ...new Set([
        ...Object.keys(ratioMap),
        ...Object.keys(usableMap),
        ...Object.keys(topupMap),
      ]),
    ]
  }, [watchedGroupRatio, watchedUserUsableGroups, watchedTopupGroupRatio])

  return (
    <div className='space-y-6'>
      <div className='flex flex-wrap justify-end gap-2'>
        <Button variant='outline' size='sm' onClick={() => setGuideOpen(true)}>
          <HelpCircle className='mr-2 h-4 w-4' />
          {t('Usage guide')}
        </Button>
        <Button variant='outline' size='sm' onClick={toggleEditMode}>
          {editMode === 'visual' ? (
            <>
              <Code2 className='mr-2 h-4 w-4' />
              {t('Switch to JSON')}
            </>
          ) : (
            <>
              <Eye className='mr-2 h-4 w-4' />
              {t('Switch to Visual')}
            </>
          )}
        </Button>
      </div>

      <GroupPricingGuide open={guideOpen} onOpenChange={setGuideOpen} />

      <Form {...form}>
        <SettingsPageActionsPortal>
          <Button
            type='button'
            size='sm'
            onClick={form.handleSubmit(onSave)}
            disabled={isSaving}
          >
            {isSaving ? t('Saving...') : t('Save group ratios')}
          </Button>
        </SettingsPageActionsPortal>
        {editMode === 'visual' ? (
          <div className='space-y-6'>
            <GroupRatioVisualEditor
              groupRatio={form.watch('GroupRatio')}
              topupGroupRatio={form.watch('TopupGroupRatio')}
              userUsableGroups={form.watch('UserUsableGroups')}
              groupGroupRatio={form.watch('GroupGroupRatio')}
              autoGroups={form.watch('AutoGroups')}
              maxTokenAutoGroupsField={
                <FormField
                  control={form.control}
                  name='MaxTokenAutoGroups'
                  render={({ field, fieldState }) => (
                    <FormItem data-invalid={fieldState.invalid}>
                      <FormLabel>
                        {t('Maximum custom groups per token')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...safeNumberFieldProps(field)}
                          type='number'
                          min={1}
                          step={1}
                          aria-invalid={fieldState.invalid}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Limits only token-specific Auto snapshots. Global Auto inheritance remains unlimited.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              }
              groupSpecialUsableGroup={form.watch('GroupSpecialUsableGroup')}
              onChange={(field, value) =>
                handleFieldChange(field as keyof GroupFormValues, value)
              }
            />

            <GroupSpecialUsableRulesEditor
              value={form.watch('GroupSpecialUsableGroup')}
              groupOptions={groupNames}
              onChange={(value) =>
                handleFieldChange('GroupSpecialUsableGroup', value)
              }
            />

            <FormField
              control={form.control}
              name='DefaultUseAutoGroup'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Default to auto groups')}</FormLabel>
                    <FormDescription>
                      {t(
                        '启用后，新建令牌默认选择 auto，调用时按有权限的自动分组顺序选路；不是固定选择第一个组。'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </div>
        ) : (
          <SettingsForm onSubmit={form.handleSubmit(onSave)}>
            <FormField
              control={form.control}
              name='GroupRatio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Group ratios')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'JSON map of group → ratio applied when the user selects the group explicitly.',
                      {
                        defaultValue:
                          'JSON 对象：路由组 → 基础倍率。显式选择、继承用户组及 auto 实际选中的组均适用；命中组间特殊倍率时由其替代，再乘逐模型优惠。',
                      }
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='TopupGroupRatio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Top-up group ratios')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Optional multiplier per user group used when calculating recharge pricing. Provide a JSON object such as'
                    )}
                    {` { "default": 1, "vip": 1.2 }`}.
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='UserUsableGroups'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Selectable groups')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'JSON map of group → description exposed when users create API keys.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='GroupGroupRatio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Inter-group overrides')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Nested JSON: source group →')}{' '}
                    {`{ targetGroup: ratio }`}{' '}
                    {t(
                      'to override billing when a user in one group uses a token of another group.',
                      {
                        defaultValue:
                          '按 用户组 → 实际计费组 覆盖组倍率；两者相同也可配置，不覆盖逐模型优惠。',
                      }
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='AutoGroups'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Auto assignment order')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      '自动分组的有序候选列表。令牌使用 auto 时，按权限过滤后的顺序查找可用渠道；不是轮询分配，也不会自动比较采购成本。'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='MaxTokenAutoGroups'
              render={({ field, fieldState }) => (
                <FormItem data-invalid={fieldState.invalid}>
                  <FormLabel>{t('Maximum custom groups per token')}</FormLabel>
                  <FormControl>
                    <Input
                      {...safeNumberFieldProps(field)}
                      type='number'
                      min={1}
                      step={1}
                      aria-invalid={fieldState.invalid}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Limits only token-specific Auto snapshots. Global Auto inheritance remains unlimited.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='GroupSpecialUsableGroup'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Special usable group rules')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Nested JSON defining per-group rules for adding (+:), removing (-:), or appending usable groups.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='DefaultUseAutoGroup'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Default to auto groups')}</FormLabel>
                    <FormDescription>
                      {t(
                        '启用后，新建令牌默认选择 auto，调用时按有权限的自动分组顺序选路；不是固定选择第一个组。'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </SettingsForm>
        )}
      </Form>
    </div>
  )
})

type GroupPricingGuideProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function GuideCodeBlock({ children }: { children: string }) {
  return (
    <pre className='bg-muted/60 overflow-x-auto rounded-lg border px-3 py-2 text-xs leading-6 whitespace-pre-wrap'>
      {children}
    </pre>
  )
}

function GuideStepRow({
  chip,
  children,
}: {
  chip: string
  children: ReactNode
}) {
  return (
    <div className='flex items-start gap-2.5 text-sm leading-6'>
      <span className='bg-muted text-muted-foreground mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-xs font-medium'>
        {chip}
      </span>
      <span className='text-muted-foreground min-w-0'>{children}</span>
    </div>
  )
}

function GroupPricingGuide({ open, onOpenChange }: GroupPricingGuideProps) {
  const { t } = useTranslation()

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Group pricing usage guide')}</SheetTitle>
          <SheetDescription>
            {t(
              'Understand how user groups, token groups, ratios, and special rules work together.'
            )}
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName('gap-5')}>
          <section className='space-y-2'>
            <h3 className='text-sm font-semibold'>
              {t('The two roles of a group')}
            </h3>
            <div className='text-muted-foreground space-y-2 text-sm leading-6'>
              <p>
                {t(
                  'Every group name in the pricing table can be used in two places: on a user (the user group, assigned by admins) and on a token (the token group, chosen when creating the token). Same name pool, two different jobs.'
                )}
              </p>
              <p>
                <span className='text-foreground font-medium'>
                  {t('Token group')}
                </span>
                {': '}
                {t(
                  'decides which channels are used and which base ratio applies.'
                )}
              </p>
              <p>
                <span className='text-foreground font-medium'>
                  {t('User group')}
                </span>
                {': '}
                {t(
                  'decides the top-up ratio, which groups the user can pick for tokens, and whether an override ratio applies.'
                )}
              </p>
            </div>
          </section>

          <section className='space-y-2'>
            <h3 className='text-sm font-semibold'>
              {t('渠道、模型与客户优惠分开配置')}
            </h3>
            <p className='text-muted-foreground text-sm leading-6'>
              {t('一个渠道可以加入多个分组，也可以提供多个模型。系统按渠道声明的分组和模型建立可用路由；上游只有一个模型，不代表渠道只能选择一个分组。')}
            </p>
            <p className='text-muted-foreground text-sm leading-6'>
              {t('同一模型给不同客户不同价格，建议共用路由组，在逐模型优惠中设置用户组优惠或个人优惠。优惠匹配客户端原始模型 ID，与上游模型映射名称无关。特殊组倍率和模型优惠会相乘，注意避免重复让利。')}
            </p>
            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                '模型映射负责上游名称适配；渠道优先级负责选路顺序（高优先级优先，同级按权重随机），重试可降到下一优先级。它们不记录采购价，采购成本不参与自动选路。充值倍率用于充值，不能当调用折扣。真人素材还必须满足对应上游账号的访问条件，不能盲目跨渠道重试。'
              )}
            </p>
          </section>

          <section className='space-y-2'>
            <h3 className='text-sm font-semibold'>
              {t('How a call is priced')}
            </h3>
            <ol className='text-muted-foreground list-decimal space-y-2 pl-5 text-sm leading-6'>
              <li>
                <span className='text-foreground font-medium'>
                  {t('Find the billing group.')}
                </span>{' '}
                {t(
                  'Key 指定组时使用该组；留空继承用户组。auto 使用 Key 自定义候选列表或全局列表，按当前权限过滤后的顺序查找该模型的可用渠道，以实际选中的组计费；失败后的跨组重试还受 Key 开关及重试策略控制。'
                )}
              </li>
              <li>
                <span className='text-foreground font-medium'>
                  {t('Find the ratio.')}
                </span>{' '}
                {t(
                  'Look for a special ratio rule matching this user group and this billing group. If one exists, use its ratio. Otherwise use the billing group base ratio from the pricing table.'
                )}
              </li>
              <li>
                <span className='text-foreground font-medium'>
                  {t('Charge.')}
                </span>{' '}
                {t(
                  '按原有模型计费方式计算用量金额，再乘上述有效组倍率和逐模型优惠系数。个人模型优惠优先于用户组模型优惠，两者不叠乘；都未设置则优惠系数为 1。'
                )}
              </li>
            </ol>
            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                'Common pitfall: the user group base ratio is NOT a personal discount. It only applies when the user group itself is the billing group.'
              )}
            </p>
          </section>

          <section className='space-y-3'>
            <h3 className='text-sm font-semibold'>{t('Worked example')}</h3>
            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                'The admin configured three groups and one special ratio rule:'
              )}
            </p>

            <div className='overflow-hidden rounded-lg border'>
              <div className='bg-muted/40 border-b px-3 py-1.5 text-xs font-medium'>
                {t('Pricing groups')}
              </div>
              <table className='w-full text-sm'>
                <thead>
                  <tr className='text-muted-foreground border-b text-xs'>
                    <th className='px-3 py-1.5 text-left font-medium'>
                      {t('Group name')}
                    </th>
                    <th className='px-3 py-1.5 text-right font-medium'>
                      {t('Ratio')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr className='border-b'>
                    <td className='px-3 py-1.5'>default</td>
                    <td className='px-3 py-1.5 text-right'>1.0</td>
                  </tr>
                  <tr className='border-b'>
                    <td className='px-3 py-1.5'>premium</td>
                    <td className='px-3 py-1.5 text-right'>0.5</td>
                  </tr>
                  <tr>
                    <td className='px-3 py-1.5'>vip</td>
                    <td className='px-3 py-1.5 text-right'>0.8</td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div className='overflow-hidden rounded-lg border'>
              <div className='bg-muted/40 border-b px-3 py-1.5 text-xs font-medium'>
                {t('Special ratio rules')}
              </div>
              <div className='p-3 text-sm leading-6'>
                {t('Users of vip, when billed as premium, pay ratio')}{' '}
                <span className='bg-primary/10 ring-primary/40 rounded px-1.5 py-0.5 font-semibold ring-1'>
                  0.3
                </span>{' '}
                <span className='text-muted-foreground text-xs'>
                  {t('(instead of {{ratio}})', { ratio: 0.5 })}
                </span>
              </div>
            </div>

            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                '同一位 vip 用户的三次调用。以下数字仅作演示，假设一次调用基础金额为 10，且未设置任何逐模型优惠。'
              )}
            </p>

            <div className='space-y-3'>
              <div className='overflow-hidden rounded-lg border'>
                <div className='bg-muted/40 border-b px-3 py-2 text-sm font-medium'>
                  {t('Call 1: the token group is premium')}
                </div>
                <div className='space-y-2 p-3'>
                  <GuideStepRow chip='1'>
                    {t(
                      'Billing group = premium (the token has a group, so use it)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='2'>
                    {t(
                      'There is a rule for vip billed as premium → use its ratio 0.3'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='='>
                    <span className='text-foreground font-medium'>
                      {t('Cost = 10 × 0.3 = 3')}
                    </span>
                  </GuideStepRow>
                </div>
              </div>

              <div className='overflow-hidden rounded-lg border'>
                <div className='bg-muted/40 border-b px-3 py-2 text-sm font-medium'>
                  {t('Call 2: the token group is default')}
                </div>
                <div className='space-y-2 p-3'>
                  <GuideStepRow chip='1'>
                    {t(
                      'Billing group = default (the token has a group, so use it)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='2'>
                    {t(
                      'No rule for vip billed as default → use the base ratio of default, 1.0 (the 0.8 of vip is not used)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='='>
                    <span className='text-foreground font-medium'>
                      {t('Cost = 10 × 1.0 = 10')}
                    </span>
                  </GuideStepRow>
                </div>
              </div>

              <div className='overflow-hidden rounded-lg border'>
                <div className='bg-muted/40 border-b px-3 py-2 text-sm font-medium'>
                  {t('Call 3: the token has no group')}
                </div>
                <div className='space-y-2 p-3'>
                  <GuideStepRow chip='1'>
                    {t(
                      'Billing group = vip (the token has no group, so use the user group)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='2'>
                    {t(
                      'No rule for vip billed as vip → use the base ratio of vip, 0.8'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='='>
                    <span className='text-foreground font-medium'>
                      {t('Cost = 10 × 0.8 = 8')}
                    </span>
                  </GuideStepRow>
                </div>
              </div>
            </div>
          </section>

          <Accordion className='rounded-lg border px-3'>
            <AccordionItem value='groups'>
              <AccordionTrigger>{t('Pricing group example')}</AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Use the pricing group table to manage the ratio and whether the group appears in the token creation dropdown.'
                  )}
                </p>
                <GuideCodeBlock>
                  {`${t('Group name')}   ${t('Ratio')}   ${t('User selectable')}   ${t('Description')}
standard     1.0     ${t('Yes')}               ${t('Standard price')}
premium      0.5     ${t('Yes')}               ${t('Premium plan, half price')}
vip          0.5     ${t('No')}                ${t('不在全局可选列表；所属用户仍可选，其他用户可由特殊规则开放')}`}
                </GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    '可选组由全局可选组和用户组特殊规则共同决定；系统还会补回用户自身的组。显式选择的路由组必须仍有有效倍率配置。'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>

            <AccordionItem value='auto'>
              <AccordionTrigger>{t('Auto group behavior')}</AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'When a token uses the auto group, the system tries groups from top to bottom until it finds an available group.',
                    {
                      defaultValue:
                        '令牌使用 auto 时，按权限过滤后的顺序查找可用渠道；失败后的跨组重试还受令牌开关及重试策略控制。不是轮询，也不比较采购成本。',
                    }
                  )}
                </p>
                <GuideCodeBlock>{`["default", "vip"]`}</GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'If default auto group is enabled, newly created tokens start with auto instead of an empty group.'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>

            <AccordionItem value='special-ratio'>
              <AccordionTrigger>{t('Special ratio rules')}</AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users pay 0.8 when billed as standard, and 0.3 when billed as premium.',
                    {
                      defaultValue:
                        '在 JSON 中，用户组为外层键，实际计费组为内层键。例如：vip 用户使用 standard 时的组倍率替换为 0.8，使用 premium 时替换为 0.3；最终金额还需乘逐模型优惠系数。',
                    }
                  )}
                </p>
                <GuideCodeBlock>{`{
  "vip": {
    "standard": 0.8,
    "premium": 0.3
  }
}`}</GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Only configured combinations are overridden. All other calls keep the billing group base ratio.'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>

            <AccordionItem value='usable'>
              <AccordionTrigger>
                {t('Special usable group rules')}
              </AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Special usable group rules make extra token groups visible to, or hide default ones from, users of a specific user group.'
                  )}
                </p>
                <GuideCodeBlock>{`{
  "vip": {
    "+:premium": "${t('Premium plan, half price')}",
    "-:default": "remove",
    "special": "${t('Special group')}"
  }
}`}</GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'In the visual editor these appear as Extra visible and Hidden. In JSON, +: (or no prefix) adds a group and -: removes one.'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>
          </Accordion>
        </div>
      </SheetContent>
    </Sheet>
  )
}
