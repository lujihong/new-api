import { useCallback, useEffect, useRef, useState } from 'react'
import { ShieldCheck, Plus, RefreshCw, Copy, Check, Trash2, FolderPlus, ChevronLeft, ChevronRight } from 'lucide-react'
import { toast } from 'sonner'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { listAssetGroups, deleteAssetGroup, listAssets, deleteAsset, type AssetScope, listChannels, type AiccChannel } from './api'
import { useAuthStore } from '@/stores/auth-store'
import type { AssetGroup, Asset } from './types'
import { usePagedList } from './use-paged-list'
import { RealPersonAuthDialog } from './components/real-person-auth-dialog'
import { CreateAssetGroupDialog } from './components/create-asset-group-dialog'
import { CreateAssetDialog } from './components/create-asset-dialog'

type GroupType = AssetGroup['groupType']

function useLifetime() {
  const alive = useRef(false)
  const request = useRef<AbortController | null>(null)
  useEffect(() => {
    alive.current = true
    request.current = new AbortController()
    return () => { alive.current = false; request.current?.abort() }
  }, [])
  return { alive, request }
}

function Pagination({ label, list }: {
  label: string
  list: Pick<ReturnType<typeof usePagedList>, 'pageNo' | 'total' | 'loading' | 'hasNext' | 'goTo'>
}) {
  return (
    <nav aria-label={`${label}分页`} className="flex min-w-0 flex-wrap items-center justify-between gap-2 border-t py-3 text-xs">
      <span className="text-muted-foreground">
        第 {list.pageNo} 页{list.total === undefined ? ' · 总数未知' : ` · 共 ${list.total} 条`}
      </span>
      <div className="flex items-center gap-1">
        <Button size="icon" variant="outline" className="size-8" aria-label={`${label}上一页`} title="上一页"
          disabled={list.loading || list.pageNo <= 1} onClick={() => list.goTo(list.pageNo - 1)}>
          <ChevronLeft className="size-4" />
        </Button>
        <Button size="icon" variant="outline" className="size-8" aria-label={`${label}下一页`} title="下一页"
          disabled={list.loading || !list.hasNext} onClick={() => list.goTo(list.pageNo + 1)}>
          <ChevronRight className="size-4" />
        </Button>
      </div>
    </nav>
  )
}

function ListError({ message, retry }: { message: string; retry: () => void }) {
  return (
    <div role="alert" className="space-y-3 py-8 text-sm">
      <p className="break-words text-destructive">{message}</p>
      <Button size="sm" variant="outline" onClick={retry}><RefreshCw className="size-3.5" />重试</Button>
    </div>
  )
}

function MediaPreview({ asset }: { asset: Asset }) {
  const [failed, setFailed] = useState(false)
  const type = asset.assetType
  const validUrl = !!asset.assetUrl && /^https?:\/\//i.test(asset.assetUrl)
  return (
    <div className="flex aspect-video min-w-0 items-center justify-center overflow-hidden rounded bg-muted">
      {(!validUrl || failed) && (
        <p className="px-3 text-center text-xs text-muted-foreground">
          {failed ? '预览加载失败，链接可能已失效' : '暂无可用预览链接'}
        </p>
      )}
      {validUrl && !failed && type === 'Image' && (
        <img src={asset.assetUrl} alt={asset.assetName} loading="lazy" onError={() => setFailed(true)} className="h-full w-full object-contain" />
      )}
      {validUrl && !failed && type === 'Video' && (
        <video src={asset.assetUrl} aria-label={`${asset.assetName}视频预览`} controls playsInline preload="metadata"
          onError={() => setFailed(true)} className="h-full w-full min-w-0 object-contain" />
      )}
      {validUrl && !failed && type === 'Audio' && (
        <audio src={asset.assetUrl} aria-label={`${asset.assetName}音频预览`} controls preload="metadata"
          onError={() => setFailed(true)} className="w-full min-w-0 max-w-full" />
      )}
      {validUrl && !failed && !['Image', 'Video', 'Audio'].includes(type) && <p className="text-xs text-muted-foreground">暂不支持此类型预览</p>}
    </div>
  )
}

const statusLabels: Record<string, string> = {
  ACTIVE: '可用', PROCESSING: '处理中', PENDING: '待处理', FAILED: '失败', EXPIRED: '已过期', DELETED: '已删除', UNKNOWN: '未知',
}

function AssetList({ channelId, group, activeTab, scope, onDeleteGroup, onCreate }: {
  channelId: number
  scope: AssetScope
  group: AssetGroup
  activeTab: GroupType
  onDeleteGroup: () => void
  onCreate: () => void
}) {
  const load = useCallback((pageNo: number, pageSize: number, signal: AbortSignal) =>
    listAssets({ groupIds: group.groupId, groupType: activeTab, pageNo, pageSize, scope, channelId }, signal), [group.groupId, activeTab, scope, channelId])
  const list = usePagedList(load)
  const { alive, request } = useLifetime()
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const [copyingId, setCopyingId] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const copySequence = useRef(0)
  useEffect(() => () => { clearTimeout(timer.current); copySequence.current++ }, [])
  const [copyPage, setCopyPage] = useState(list.pageNo)
  if (copyPage !== list.pageNo) {
    setCopyPage(list.pageNo)
    setCopiedId(null)
    setCopyingId(null)
  }
  useEffect(() => {
    copySequence.current++
    clearTimeout(timer.current)
  }, [list.pageNo])

  const copyAssetUri = async (asset: Asset) => {
    if (scope !== 'personal' || asset.status?.toUpperCase() !== 'ACTIVE') return
    const sequence = ++copySequence.current
    setCopyingId(asset.assetId)
    clearTimeout(timer.current)
    setCopiedId(null)
    try {
      await navigator.clipboard.writeText(`asset://${asset.assetId}`)
      if (!alive.current || sequence !== copySequence.current) return
      setCopiedId(asset.assetId)
      toast.success('素材 URI 已复制')
      timer.current = setTimeout(() => { if (alive.current) setCopiedId(null) }, 2000)
    } catch {
      if (alive.current && sequence === copySequence.current) toast.error('复制失败，请检查剪贴板权限后重试')
    } finally {
      if (alive.current && sequence === copySequence.current) setCopyingId(null)
    }
  }

  const removeAsset = async (asset: Asset) => {
    if (!confirm(`确定要删除素材【${asset.assetName}】吗？`)) return
    if (scope === 'management' && !confirm(`管理操作二次确认：将删除他人或历史素材 ${asset.assetId}，不会将其认领为本人素材。确认继续？`)) return
    setDeletingId(asset.assetId)
    try {
      await deleteAsset(asset.assetId, channelId, scope, request.current?.signal)
      if (!alive.current) return
      toast.success('素材已删除')
      if (list.data.length === 1 && list.pageNo > 1) list.goTo(list.pageNo - 1)
      else list.refresh()
    } catch (error) {
      if (alive.current) toast.error(`删除失败：${error instanceof Error ? error.message : '网络异常'}`)
    } finally {
      if (alive.current) setDeletingId(null)
    }
  }

  return (
      <section aria-label="素材列表" className="min-w-0">
        <header className="mb-3 flex flex-wrap items-start justify-between gap-3 border-b pb-3">
          <div className="min-w-0 flex-1 basis-40">
            <h3 className="break-words text-sm font-semibold">{group.groupName}</h3>
            <p className="break-all font-mono text-xs text-muted-foreground">{group.groupId}</p>
            {group.description && <p className="mt-1 break-words text-xs text-muted-foreground">{group.description}</p>}
          </div>
          <div className="flex flex-wrap items-center gap-1">
            <Button size="icon" variant="ghost" className="size-8" aria-label="刷新素材" title="刷新素材" onClick={list.refresh} disabled={list.loading}>
              <RefreshCw className="size-3.5" />
            </Button>
            {scope === 'personal' && <Button size="sm" variant="outline" onClick={onCreate}><Plus className="size-3.5" />添加素材</Button>}
            <Button size="icon" variant="ghost" className="size-8" onClick={onDeleteGroup} aria-label="删除素材组" title="删除素材组"><Trash2 className="size-3.5" /></Button>
          </div>
        </header>
        {list.loading && <p role="status" className="py-12 text-center text-sm text-muted-foreground">正在加载素材…</p>}
        {!list.loading && list.error && <ListError message={`加载素材失败：${list.error}`} retry={list.refresh} />}
        {!list.loading && !list.error && list.data.length === 0 && <p className="py-12 text-center text-sm text-muted-foreground">{list.pageNo > 1 ? '本页暂无素材，可返回上一页' : '该素材组下暂无入库素材'}</p>}
        {!list.loading && !list.error && list.data.length > 0 && <div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {list.data.map((asset) => {
              const status = asset.status?.toUpperCase() || 'UNKNOWN'
              let copyLabel = '复制 URI'
              if (copyingId === asset.assetId) copyLabel = '复制中…'
              if (copiedId === asset.assetId) copyLabel = '已复制 URI'
              return (
                <article key={asset.assetId} aria-label={asset.assetName} className="min-w-0 space-y-2 rounded-md border p-2.5">
                  <MediaPreview key={`${asset.assetId}:${asset.assetUrl}`} asset={asset} />
                  <div className="flex min-w-0 items-center justify-between gap-2">
                    <h4 className="min-w-0 truncate text-sm font-medium" title={asset.assetName}>{asset.assetName}</h4>
                    <Badge variant="secondary" className="shrink-0 text-[10px]">{statusLabels[status] ?? '未知'}</Badge>
                  </div>
                  <p className="truncate font-mono text-xs text-muted-foreground" title={asset.assetId}>{asset.assetId}</p>
                  <div className="flex min-w-0 items-center justify-between gap-1 border-t pt-2">
                    <Button size="sm" variant="secondary" className="min-w-0 text-xs" onClick={() => void copyAssetUri(asset)}
                      disabled={scope !== 'personal' || status !== 'ACTIVE' || copyingId !== null} aria-label={`复制 ${asset.assetName} URI`}>
                      {copiedId === asset.assetId ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                      {copyLabel}
                    </Button>
                    <Button size="icon" variant="ghost" className="size-8 shrink-0" title="删除素材" aria-label={`删除素材 ${asset.assetName}`}
                      disabled={deletingId !== null} onClick={() => void removeAsset(asset)}><Trash2 className="size-3.5" /></Button>
                  </div>
                </article>
              )
            })}
          </div>}
        <Pagination label="素材" list={list} />
      </section>
  )
}

function AssetWorkspace({ channelId, activeTab, onTabChange, scope, onScopeChange, isAdmin }: {
  channelId: number
  activeTab: GroupType; onTabChange: (tab: GroupType) => void
  scope: AssetScope; onScopeChange: (scope: AssetScope) => void; isAdmin: boolean
}) {
  const load = useCallback((pageNo: number, pageSize: number, signal: AbortSignal) =>
    listAssetGroups({ groupType: activeTab, pageNo, pageSize, scope, channelId }, signal), [activeTab, scope, channelId])
  const list = usePagedList(load)
  const { alive, request } = useLifetime()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const selectedGroup = list.data.find((group) => group.groupId === selectedId) ?? list.data[0]
  const [assetDialogGroup, setAssetDialogGroup] = useState<AssetGroup | null>(null)
  const [assetRevision, setAssetRevision] = useState(0)
  const [authOpen, setAuthOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const deleting = useRef(false)
  let selectionHint = '请选择素材组查看详情'
  if (list.error) selectionHint = '素材组加载失败，请重试'
  if (list.loading) selectionHint = '等待素材组加载'

  const removeGroup = async () => {
    if (!selectedGroup || deleting.current || !confirm(`确定要删除素材组【${selectedGroup.groupName}】吗？删除后不可恢复。`)) return
    if (scope === 'management' && !confirm(`管理操作二次确认：将删除素材组 ${selectedGroup.groupId}，可能影响其所有者，且不可恢复。确认继续？`)) return
    deleting.current = true
    try {
      await deleteAssetGroup(selectedGroup.groupId, channelId, scope, request.current?.signal)
      if (!alive.current) return
      toast.success('素材组已删除')
      if (list.data.length === 1 && list.pageNo > 1) list.goTo(list.pageNo - 1)
      else list.refresh()
    } catch (error) {
      if (alive.current) toast.error(`删除失败：${error instanceof Error ? error.message : '网络异常'}`)
    } finally {
      deleting.current = false
    }
  }

  return (
    <>
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>{scope === 'personal' ? '我的素材' : '管理全部素材'}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button variant="outline" size="icon" className="size-8" onClick={list.refresh} disabled={list.loading} aria-label="刷新素材组" title="刷新素材组"><RefreshCw className="size-3.5" /></Button>
            {isAdmin && <Button variant="outline" size="sm" onClick={() => onScopeChange(scope === 'personal' ? 'management' : 'personal')}>
              {scope === 'personal' ? '管理全部素材' : '返回我的素材'}
            </Button>}
            {scope === 'personal' && <>
              {activeTab === 'AIGC' && <Button variant="outline" size="sm" onClick={() => setCreateOpen(true)} aria-label="新建虚拟素材组"><FolderPlus className="size-3.5" />新建组</Button>}
              <Button size="sm" onClick={() => setAuthOpen(true)} aria-label="真人实名认证 (扫码)"><ShieldCheck className="size-3.5" />扫码认证</Button>
            </>}
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className="min-w-0 space-y-4 pb-8">
            {scope === 'management' ? <p role="note" className="rounded-md border-2 border-amber-500 bg-amber-50 p-4 text-sm font-medium text-amber-950">
              仅管理：可查看与删除全部素材，不会自动认领。此处禁止创建、认证、添加个人素材及复制用于生成；个人操作请返回“我的素材”。
            </p> : <p className="text-sm text-muted-foreground">真人与虚拟素材均仅显示本人所有；管理员新建与认证也归本人。同一人的新照片每次仍需一致性校验，不保证任意人或任意图片可入库。</p>}
            <Tabs value={activeTab} onValueChange={(value) => { if (value === 'AIGC' || value === 'LivenessFace') onTabChange(value) }}>
              <TabsList className="h-9 max-w-full">
                <TabsTrigger value="LivenessFace" aria-label="真人素材库 (LivenessFace)">真人素材</TabsTrigger>
                <TabsTrigger value="AIGC" aria-label="虚拟人像素材库 (AIGC)">虚拟素材</TabsTrigger>
              </TabsList>
            </Tabs>
            <div className="grid min-w-0 grid-cols-1 items-start gap-4 md:grid-cols-[minmax(0,14rem)_minmax(0,1fr)]">
              <section aria-label="素材组列表" className="min-w-0 md:border-r md:pr-4">
                <h3 className="mb-2 text-xs font-semibold text-muted-foreground">素材组</h3>
                {list.loading && <p role="status" className="py-8 text-center text-sm text-muted-foreground">正在加载素材组…</p>}
                {!list.loading && list.error && <ListError message={`加载素材组失败：${list.error}`} retry={list.refresh} />}
                {!list.loading && !list.error && list.data.length === 0 && <p className="py-8 text-sm text-muted-foreground">{list.pageNo > 1 ? '本页暂无素材组，可返回上一页' : '暂无素材组'}</p>}
                {!list.loading && !list.error && list.data.length > 0 && <div className="space-y-1">
                    {list.data.map((group) => <button key={group.groupId} type="button" aria-pressed={selectedGroup?.groupId === group.groupId}
                      className={`block w-full min-w-0 rounded px-2.5 py-2 text-left hover:bg-muted ${selectedGroup?.groupId === group.groupId ? 'bg-muted' : ''}`}
                      onClick={() => setSelectedId(group.groupId)}>
                      <span className="block truncate text-sm font-medium" title={group.groupName}>{group.groupName}</span>
                      <span className="block truncate font-mono text-xs text-muted-foreground" title={group.groupId}>{group.groupId}</span>
                    </button>)}
                  </div>}
                <Pagination label="素材组" list={list} />
              </section>
              {selectedGroup ? <AssetList channelId={channelId} key={`${selectedGroup.groupId}:${assetRevision}`} group={selectedGroup} activeTab={activeTab} scope={scope} onDeleteGroup={() => void removeGroup()} onCreate={() => setAssetDialogGroup(selectedGroup)} />
                : <p className="min-w-0 py-12 text-center text-sm text-muted-foreground">{selectionHint}</p>}
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {scope === 'personal' && <>
      <RealPersonAuthDialog channelId={channelId} open={authOpen} onOpenChange={setAuthOpen} onSuccess={list.refresh} />
      <CreateAssetGroupDialog channelId={channelId} open={createOpen} onOpenChange={setCreateOpen} onSuccess={list.refresh} />
      {assetDialogGroup && <CreateAssetDialog channelId={channelId} open groupId={assetDialogGroup.groupId} groupName={assetDialogGroup.groupName}
        onOpenChange={(open) => { if (!open) setAssetDialogGroup(null) }} onSuccess={() => setAssetRevision((value) => value + 1)} />}
      </>}
    </>
  )
}

function IdentityWorkspace({ isAdmin }: { isAdmin: boolean }) {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState<GroupType>('LivenessFace')
  const [scope, setScope] = useState<AssetScope>('personal')
  const [channels, setChannels] = useState<AiccChannel[]>([])
  const [channelId, setChannelId] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const effectiveScope = isAdmin ? scope : 'personal'
  useEffect(() => {
    const controller = new AbortController()
    void listChannels(controller.signal).then((data) => {
      if (controller.signal.aborted) return
      setChannels(data)
      setChannelId(data.length === 1 ? data[0].id : null)
      setLoading(false)
    }, (failure: unknown) => {
      if (controller.signal.aborted) return
      setError(failure instanceof Error ? failure.message : 'AICC channel request failed')
      setLoading(false)
    })
    return () => controller.abort()
  }, [revision])
  const retry = () => { setLoading(true); setError(''); setChannels([]); setChannelId(null); setRevision(value => value + 1) }
  const selected = channels.find(channel => channel.id === channelId)
  const items = channels.map(channel => ({ value: channel.id, label: `${channel.name} · #${channel.id}` }))
  if (loading) return <div role="status"><LoadingState message={t('Loading AICC channels', { defaultValue: '正在加载 AICC 渠道…' })} /></div>
  if (error) return <ErrorState title={t('Failed to load AICC channels', { defaultValue: '加载 AICC 渠道失败' })} description={error} onRetry={retry} />
  if (!channels.length) { return <EmptyState title={t('No AICC channels available', { defaultValue: '暂无可用 AICC 渠道' })}
    action={<Button variant="outline" onClick={retry}>{t('Retry', { defaultValue: '重试' })}</Button>} /> }
  return <>
    <section aria-label={t('AICC channel', { defaultValue: 'AICC 渠道' })} className="min-w-0 space-y-2 px-4 py-3">
      {channels.length > 1 && <Select items={items} value={channelId} onValueChange={(value) => {
        if (channels.some(channel => channel.id === value)) setChannelId(value)
      }}>
        <SelectTrigger aria-label={t('Select AICC channel', { defaultValue: '选择 AICC 渠道' })} className="w-full sm:w-80">
          <SelectValue placeholder={t('Select AICC channel', { defaultValue: '选择 AICC 渠道' })} />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          {items.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}
        </SelectContent>
      </Select>}
      {selected && <p className="break-words text-sm">{t('Channel: {{name}} · #{{id}} · Source: China Mobile AICC · Region: {{region}}', {
        defaultValue: '渠道：{{name}} · #{{id}} · 来源：移动云 AICC · 节点地区：{{region}}',
        name: selected.name, id: selected.id, region: selected.region || t('Unknown', { defaultValue: '未知' }),
      })}</p>}
      <p className="text-xs text-muted-foreground">{t('Asset URIs are only available to the current user in the owning upstream account; region does not identify an account and cross-channel use is not guaranteed.', {
        defaultValue: 'asset:// URI 仅当前用户在素材所属上游账号内可用；地区不代表账号，不保证跨渠道通用。',
      })}</p>
    </section>
    {selected ? <AssetWorkspace key={`${selected.id}:${activeTab}:${effectiveScope}`} channelId={selected.id}
      activeTab={activeTab} onTabChange={setActiveTab} scope={effectiveScope} onScopeChange={setScope} isAdmin={isAdmin} />
      : <EmptyState title={t('Select a channel to view assets', { defaultValue: '请选择渠道后查看素材' })} />}
  </>
}

export function AiccAssets() {
  const user = useAuthStore((state) => state.auth.user)
  return <IdentityWorkspace key={`${user?.id ?? 'anonymous'}:${user?.role ?? 0}`} isAdmin={(user?.role ?? 0) >= 10} />
}
