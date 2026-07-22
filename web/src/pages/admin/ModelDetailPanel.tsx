import { useQuery } from '@tanstack/react-query';
import { Skeleton } from '@/components/ui/skeleton';
import {
  getModel,
  MODEL_STATUS_LABELS,
  MODEL_TYPE_LABELS,
  MODEL_TIER_LABELS,
  statusBadgeClass,
  formatPricing,
  formatContextWindow,
} from '@/lib/model-management';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

interface ModelDetailPanelProps {
  modelPk: number | null;
  onClose: () => void;
}

export function ModelDetailPanel({ modelPk, onClose }: ModelDetailPanelProps) {
  const { data, isLoading } = useQuery({
    queryKey: ['admin-model-detail', modelPk],
    queryFn: () => getModel(modelPk!),
    enabled: modelPk != null,
  });

  const model = data?.model;
  const aliases = data?.aliases ?? [];
  const channelMappings = data?.channel_mappings ?? [];
  const subscriptionMappings = data?.subscription_mappings ?? [];

  return (
    <Dialog open={modelPk != null} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>模型详情</DialogTitle>
          <DialogDescription>
            {model ? model.model_id : '加载中…'}
          </DialogDescription>
        </DialogHeader>

        {isLoading || !model ? (
          <div className="space-y-4">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : (
          <div className="space-y-6">
            {/* Basic info */}
            <section className="grid grid-cols-2 gap-4">
              <div>
                <p className="text-xs text-muted-foreground">显示名称</p>
                <p className="font-medium">{model.display_name}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">模型 ID</p>
                <p className="font-mono text-sm">{model.model_id}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">提供商</p>
                <p>{model.provider || '—'}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">类型</p>
                <p>{MODEL_TYPE_LABELS[model.model_type] ?? model.model_type ?? '—'}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">状态</p>
                <span className={'inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ' + statusBadgeClass(model.status)}>
                  {MODEL_STATUS_LABELS[model.status] ?? String(model.status)}
                </span>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">等级</p>
                <p>{MODEL_TIER_LABELS[model.tier] ?? (model.tier || '—')}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">上下文窗口</p>
                <p>{formatContextWindow(model.context_window)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">分类</p>
                <p>{model.category || '—'}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">输入价格</p>
                <p>{formatPricing(model.pricing_input)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">输出价格</p>
                <p>{formatPricing(model.pricing_output)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">公开显示</p>
                <p>{model.is_public ? '是' : '否'}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">渠道/订阅数</p>
                <p>{model.channel_count} / {model.subscription_count}</p>
              </div>
            </section>

            {model.description && (
              <section>
                <h4 className="mb-2 text-sm font-semibold">描述</h4>
                <p className="text-sm text-muted-foreground">{model.description}</p>
              </section>
            )}

            {model.capabilities && model.capabilities.length > 0 && (
              <section>
                <h4 className="mb-2 text-sm font-semibold">能力标签</h4>
                <div className="flex flex-wrap gap-2">
                  {model.capabilities.map((cap) => (
                    <span key={cap} className="inline-flex items-center rounded-full bg-blue-100 px-2 py-1 text-xs font-medium text-blue-800 dark:bg-blue-900 dark:text-blue-200">
                      {cap}
                    </span>
                  ))}
                </div>
              </section>
            )}

            {model.tags && model.tags.length > 0 && (
              <section>
                <h4 className="mb-2 text-sm font-semibold">自定义标签</h4>
                <div className="flex flex-wrap gap-2">
                  {model.tags.map((tag) => (
                    <span key={tag} className="inline-flex items-center rounded-full bg-purple-100 px-2 py-1 text-xs font-medium text-purple-800 dark:bg-purple-900 dark:text-purple-200">
                      {tag}
                    </span>
                  ))}
                </div>
              </section>
            )}

            {/* Aliases */}
            {aliases.length > 0 && (
              <section>
                <h4 className="mb-2 text-sm font-semibold">别名 ({aliases.length})</h4>
                <div className="overflow-x-auto rounded-lg border">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>别名</TableHead>
                        <TableHead>主别名</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {aliases.map((a) => (
                        <TableRow key={a.id}>
                          <TableCell className="font-mono text-sm">{a.alias}</TableCell>
                          <TableCell>{a.is_primary ? '是' : '否'}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </section>
            )}

            {/* Channel mappings */}
            {channelMappings.length > 0 && (
              <section>
                <h4 className="mb-2 text-sm font-semibold">渠道映射 ({channelMappings.length})</h4>
                <div className="overflow-x-auto rounded-lg border">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>渠道 ID</TableHead>
                        <TableHead>启用</TableHead>
                        <TableHead>优先级</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {channelMappings.map((c) => (
                        <TableRow key={c.id}>
                          <TableCell className="font-mono text-sm">{c.channel_id}</TableCell>
                          <TableCell>{c.enabled ? '是' : '否'}</TableCell>
                          <TableCell>{c.priority}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </section>
            )}

            {/* Subscription mappings */}
            {subscriptionMappings.length > 0 && (
              <section>
                <h4 className="mb-2 text-sm font-semibold">订阅映射 ({subscriptionMappings.length})</h4>
                <div className="overflow-x-auto rounded-lg border">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>订阅账户 ID</TableHead>
                        <TableHead>用户组</TableHead>
                        <TableHead>启用</TableHead>
                        <TableHead>优先级</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {subscriptionMappings.map((s) => (
                        <TableRow key={s.id}>
                          <TableCell className="font-mono text-sm">{s.subscription_account_id}</TableCell>
                          <TableCell>{s.group_name}</TableCell>
                          <TableCell>{s.enabled ? '是' : '否'}</TableCell>
                          <TableCell>{s.priority}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </section>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
