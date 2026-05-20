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

interface OptionItem {
  key: string;
  value: string;
}

const FEATURED_OPTIONS = [
  {
    key: 'RegisterEnabled',
    label: 'Registration enabled',
    description: 'Allow new users to create accounts.',
    type: 'boolean',
  },
  {
    key: 'QuotaForNewUser',
    label: 'Default new-user quota',
    description: 'Quota granted when a user registers.',
    type: 'quota',
  },
  {
    key: 'QuotaForInviter',
    label: 'Inviter reward',
    description: 'Quota granted to the inviter after a successful invite.',
    type: 'quota',
  },
  {
    key: 'QuotaForInvitee',
    label: 'Invitee reward',
    description: 'Quota granted to the invited user.',
    type: 'quota',
  },
] as const;

function optionValue(options: OptionItem[] | undefined, key: string) {
  return options?.find((option) => option.key === key)?.value ?? '';
}

function quotaToDisplay(value: string) {
  const raw = Number.parseInt(value || '0', 10);
  return Number.isFinite(raw) ? (raw / 500000).toString() : '0';
}

function displayToQuota(value: string) {
  return Math.floor(Number.parseFloat(value || '0') * 500000).toString();
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
      ensureApiSuccess(res.data, 'Option update failed');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-options'] });
      toast.success('Option saved');
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
      toast.error('Option key is required');
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
        <h2 className="text-2xl font-semibold">System Options</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Manage registration, quota grants, and one-api compatible settings.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Core Settings</CardTitle>
          <CardDescription>Common operational options used by user onboarding.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={['Setting', 'Value', 'Actions']} rows={4} />
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Setting</TableHead>
                    <TableHead>Value</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
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
                            <option value="true">Enabled</option>
                            <option value="false">Disabled</option>
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
                          Save
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
          <CardTitle>Custom Option</CardTitle>
          <CardDescription>Create or overwrite any one-api option key.</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 md:grid-cols-[1fr_2fr_auto] md:items-end">
            <div className="space-y-2">
              <Label htmlFor="option-key">Key</Label>
              <Input id="option-key" value={customKey} onChange={(event) => setCustomKey(event.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="option-value">Value</Label>
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
          <CardTitle>All Options</CardTitle>
          <CardDescription>Read-only overview of values returned by the backend.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={['Key', 'Value']} rows={8} />
          ) : !options || options.length === 0 ? (
            <EmptyState title="No options found" description="System options storage may not be configured." />
          ) : (
            <div className="max-h-[420px] overflow-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Key</TableHead>
                    <TableHead>Value</TableHead>
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
