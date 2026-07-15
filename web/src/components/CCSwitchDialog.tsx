import { useEffect, useState } from 'react';
import { Bot, Cpu, Sparkles } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { API_BASE_URL } from '@/lib/api';

type AppType = 'claude' | 'codex' | 'gemini';

interface ModelField {
  key: string;
  label: string;
  required: boolean;
}

interface AppConfig {
  label: string;
  defaultName: string;
  icon: typeof Bot;
  modelFields: ModelField[];
}

const APP_CONFIGS: Record<AppType, AppConfig> = {
  claude: {
    label: 'Claude Code',
    defaultName: 'My Claude',
    icon: Bot,
    modelFields: [
      { key: 'model', label: '主模型', required: true },
      { key: 'haikuModel', label: 'Haiku 模型', required: false },
      { key: 'sonnetModel', label: 'Sonnet 模型', required: false },
      { key: 'opusModel', label: 'Opus 模型', required: false },
    ],
  },
  codex: {
    label: 'Codex',
    defaultName: 'My Codex',
    icon: Cpu,
    modelFields: [{ key: 'model', label: '主模型', required: true }],
  },
  gemini: {
    label: 'Gemini CLI',
    defaultName: 'My Gemini',
    icon: Sparkles,
    modelFields: [{ key: 'model', label: '主模型', required: true }],
  },
};

function buildCCSwitchURL(
  app: string,
  name: string,
  models: Record<string, string>,
  apiKey: string,
  baseUrl: string,
): string {
  const endpoint = app === 'codex' ? `${baseUrl}/v1` : baseUrl;
  const params = new URLSearchParams();
  params.set('resource', 'provider');
  params.set('app', app);
  params.set('name', name);
  params.set('endpoint', endpoint);
  params.set('apiKey', apiKey);
  for (const [k, v] of Object.entries(models)) {
    if (v) params.set(k, v);
  }
  params.set('homepage', baseUrl);
  params.set('enabled', 'true');
  return `ccswitch://v1/import?${params.toString()}`;
}

// Cached server address so we only fetch /api/status once per session.
let cachedServerAddress: string | null = null;

async function getServerAddress(): Promise<string> {
  if (cachedServerAddress) return cachedServerAddress;
  try {
    const res = await fetch(`${API_BASE_URL}/status`);
    const json = await res.json();
    const addr = json?.data?.server_address;
    if (addr && typeof addr === 'string' && addr.trim()) {
      cachedServerAddress = addr.replace(/\/+$/, '');
      return cachedServerAddress;
    }
  } catch {
    /* fall through */
  }
  return window.location.origin;
}

interface CCSwitchDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tokenKey?: string;
  modelOptions?: string[];
}

export function CCSwitchDialog({
  open,
  onOpenChange,
  tokenKey = '',
  modelOptions = [],
}: CCSwitchDialogProps) {
  const [app, setApp] = useState<AppType>('claude');
  const [name, setName] = useState(APP_CONFIGS.claude.defaultName);
  const [models, setModels] = useState<Record<string, string>>({});
  const [apiKey, setApiKey] = useState(
    tokenKey.startsWith('sk-') ? tokenKey : tokenKey ? `sk-${tokenKey}` : '',
  );
  const [baseUrl, setBaseUrl] = useState(window.location.origin);

  // The parent bumps a `key` prop each time the dialog opens so this is a
  // fresh component instance: useState initializers run with the new
  // tokenKey, and the effect below prefetches the configured server
  // address exactly once per session. This is the React-recommended way
  // to "reset state when a prop changes" — re-using the old approach of
  // calling onOpenChange would never fire because Base UI's controlled
  // Dialog does not echo the parent's `open=true` back through
  // `onOpenChange`.
  useEffect(() => {
    let cancelled = false;
    getServerAddress().then((addr) => {
      if (!cancelled) setBaseUrl(addr);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const handleOpenChange = (next: boolean) => {
    onOpenChange(next);
  };

  const currentConfig = APP_CONFIGS[app];

  const handleAppChange = (nextApp: AppType) => {
    setApp(nextApp);
    setName(APP_CONFIGS[nextApp].defaultName);
    setModels({});
  };

  const handleModelChange = (field: string, value: string) => {
    setModels((prev) => ({ ...prev, [field]: value }));
  };

  const handleSubmit = () => {
    if (!models.model || !apiKey) {
      return;
    }
    const url = buildCCSwitchURL(app, name, models, apiKey, baseUrl);
    window.open(url, '_blank');
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>导入到 CC Switch</DialogTitle>
          <DialogDescription>选择目标应用和模型，一键导入供应商配置到 CC Switch。</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 pt-2 max-h-[60vh] overflow-y-auto">
          {/* API Key */}
          <div className="space-y-2">
            <Label htmlFor="ccs-api-key">
              API Key
              <span className="ml-0.5 text-destructive">*</span>
            </Label>
            <Input
              id="ccs-api-key"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder="sk-..."
              className="font-mono text-xs"
            />
            <p className="text-xs text-slate-400">在「API 密钥」页面创建密钥后粘贴到此处。</p>
          </div>

          {/* Base URL */}
          <div className="space-y-2">
            <Label htmlFor="ccs-base-url">API 地址</Label>
            <Input
              id="ccs-base-url"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://your-api-domain.com"
              className="font-mono text-xs"
            />
            <p className="text-xs text-slate-400">CC Switch 会将此地址写入对应客户端的配置。</p>
          </div>

          {/* Application selector */}
          <div className="space-y-2">
            <Label>目标应用</Label>
            <div className="flex gap-2">
              {(Object.keys(APP_CONFIGS) as AppType[]).map((key) => {
                const cfg = APP_CONFIGS[key];
                const Icon = cfg.icon;
                const isActive = app === key;
                return (
                  <button
                    key={key}
                    type="button"
                    onClick={() => handleAppChange(key)}
                    className={cn(
                      'flex flex-1 items-center justify-center gap-2 rounded-lg border px-3 py-2.5 text-sm font-semibold transition-colors',
                      isActive
                        ? 'border-blue-500 bg-blue-50 text-blue-600 dark:border-blue-400 dark:bg-blue-500/10 dark:text-blue-300'
                        : 'border-slate-200 text-slate-500 hover:bg-slate-50 dark:border-white/10 dark:text-slate-400 dark:hover:bg-white/5',
                    )}
                  >
                    <Icon className="size-4" />
                    {cfg.label}
                  </button>
                );
              })}
            </div>
          </div>

          {/* Name */}
          <div className="space-y-2">
            <Label htmlFor="ccs-name">名称</Label>
            <Input
              id="ccs-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={currentConfig.defaultName}
            />
          </div>

          {/* Model fields */}
          {currentConfig.modelFields.map((field) => (
            <div key={field.key} className="space-y-2">
              <Label htmlFor={`ccs-model-${field.key}`}>
                {field.label}
                {field.required && <span className="ml-0.5 text-destructive">*</span>}
              </Label>
              <Input
                id={`ccs-model-${field.key}`}
                value={models[field.key] || ''}
                onChange={(e) => handleModelChange(field.key, e.target.value)}
                placeholder="输入或选择模型名称"
                className="font-mono text-xs"
                list={modelOptions.length > 0 ? `ccs-model-list-${field.key}` : undefined}
              />
              {modelOptions.length > 0 && (
                <datalist id={`ccs-model-list-${field.key}`}>
                  {modelOptions.map((m) => (
                    <option key={m} value={m} />
                  ))}
                </datalist>
              )}
            </div>
          ))}
        </div>
        <DialogFooter>
          <DialogClose render={<Button variant="outline" />}>取消</DialogClose>
          <Button onClick={handleSubmit} disabled={!models.model || !apiKey}>
            打开 CC Switch
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
