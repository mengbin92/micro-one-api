import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Save } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { adminApiClient } from '@/lib/api';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
import { amountUnitsToCurrencyUnits, currencyUnitsToAmountUnits } from '@/lib/amount';

interface OptionItem {
  key: string;
  value: string;
}

const FEATURED_OPTIONS = [
  {
    key: 'RegisterEnabled',
    label: '开放注册',
    description: '允许新用户注册账号。',
    type: 'boolean',
  },
  {
    key: 'QuotaForNewUser',
    label: '新用户默认额度',
    description: '新用户注册时获得的初始额度。',
    type: 'quota',
  },
  {
    key: 'QuotaForInviter',
    label: '邀请人奖励额度',
    description: '成功邀请一位新用户后,邀请人获得的奖励额度。',
    type: 'quota',
  },
  {
    key: 'QuotaForInvitee',
    label: '被邀请人奖励额度',
    description: '被邀请的新用户获得的奖励额度。',
    type: 'quota',
  },
] as const;

function optionValue(options: OptionItem[] | undefined, key: string) {
  return options?.find((option) => option.key === key)?.value ?? '';
}

function quotaToDisplay(value: string) {
  const raw = Number.parseInt(value || '0', 10);
  return Number.isFinite(raw) ? amountUnitsToCurrencyUnits(raw).toString() : '0';
}

function displayToQuota(value: string) {
  return currencyUnitsToAmountUnits(value).toString();
}

export function AdminOptionsPage() {
  const queryClient = useQueryClient();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [customKey, setCustomKey] = useState('');
  const [customValue, setCustomValue] = useState('');

  const { data: options, isLoading } = useQuery({
    queryKey: ['admin-options'],
    queryFn: async () => {
      const res = await adminApiClient.get('/option/');
      return unwrapApiData<OptionItem[]>(res.data);
    },
  });

  const updateMutation = useMutation({
    mutationFn: async ({ key, value }: OptionItem) => {
      const res = await adminApiClient.put('/option/', { key, value });
      ensureApiSuccess(res.data, '选项更新失败');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-options'] });
      toast.success('选项已保存');
    },
  });

  const featuredRows = useMemo(
    () =>
      FEATURED_OPTIONS.map((item) => {
        const current = drafts[item.key] ?? optionValue(options, item.key);
        return {
          ...item,
          value: item.type === 'quota' ? quotaToDisplay(current) : current,
        };
      }),
    [drafts, options]
  );

  const setDraft = (key: string, value: string) => {
    setDrafts((current) => ({ ...current, [key]: value }));
  };

  const saveOption = (key: string, value: string, type?: string) => {
    const normalizedValue = type === 'quota' ? displayToQuota(value) : value;
    updateMutation.mutate({ key, value: normalizedValue });
  };

  const handleCustomSave = () => {
    const key = customKey.trim();
    if (!key) {
      toast.error('请输入选项键名');
      return;
    }
    updateMutation.mutate(
      { key, value: customValue },
      {
        onSuccess: () => {
          setCustomKey('');
          setCustomValue('');
        },
      }
    );
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold">系统选项</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          管理系统注册、额度发放和兼容 one-api 的设置项。
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>核心设置</CardTitle>
          <CardDescription>用户入门相关的常用配置项。</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={['配置项', '当前值', '操作']} rows={4} />
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>配置项</TableHead>
                    <TableHead>值</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {featuredRows.map((option) => (
                    <TableRow key={option.key}>
                      <TableCell>
                        <div className="font-medium">{option.label}</div>
                        <div className="text-xs text-muted-foreground">{option.description}</div>
                      </TableCell>
                      <TableCell>
                        {option.type === 'boolean' ? (
                          <select
                            value={option.value || 'false'}
                            onChange={(event) => setDraft(option.key, event.target.value)}
                            className="h-8 rounded-md border bg-background px-2 text-sm"
                          >
                            <option value="true">启用</option>
                            <option value="false">禁用</option>
                          </select>
                        ) : (
                          <Input
                            type="number"
                            min="0"
                            step="0.01"
                            value={option.value}
                            onChange={(event) => setDraft(option.key, event.target.value)}
                            className="max-w-40"
                          />
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={updateMutation.isPending}
                          onClick={() => saveOption(option.key, option.value, option.type)}
                        >
                          <Save className="size-3.5" />
                          保存
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>自定义选项</CardTitle>
          <CardDescription>创建或覆盖任意 one-api 兼容的选项。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 md:grid-cols-[1fr_2fr_auto] md:items-end">
            <div className="space-y-2">
              <Label htmlFor="option-key">键名</Label>
              <Input id="option-key" value={customKey} onChange={(event) => setCustomKey(event.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="option-value">选项值</Label>
              <Input id="option-value" value={customValue} onChange={(event) => setCustomValue(event.target.value)} />
            </div>
            <Button onClick={handleCustomSave} disabled={updateMutation.isPending}>
              Save Option
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>全部选项</CardTitle>
          <CardDescription>查看后端返回的所有选项值（只读）。</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={['键名', '值']} rows={8} />
          ) : !options || options.length === 0 ? (
            <EmptyState title="未找到任何选项" description="系统选项存储可能尚未配置。" />
          ) : (
            <div className="max-h-[420px] overflow-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>键名</TableHead>
                    <TableHead>值</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {options.map((option) => (
                    <TableRow key={option.key}>
                      <TableCell className="font-mono text-xs">{option.key}</TableCell>
                      <TableCell className="max-w-xl break-all font-mono text-xs">{option.value || '-'}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
