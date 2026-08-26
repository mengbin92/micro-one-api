import { useState, useEffect, useCallback } from 'react';
import {
  Check,
  Copy,
  Terminal,
  Code2,
  Bot,
  FileText,
  ShieldCheck,
  Apple,
  Monitor,
  SquareTerminal,
  KeyRound,
  Globe,
  ArrowRight,
} from 'lucide-react';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { resolveRelayBaseUrl } from '@/lib/server-address';

// ---------------------------------------------------------------------------
// Types & helpers
// ---------------------------------------------------------------------------

interface CodeSnippet {
  label: string;
  language: string;
  code: string;
  notes?: string[];
}

type ShellOS = 'unix' | 'cmd' | 'powershell';

// ---------------------------------------------------------------------------
// CopyableCode — a dark code block with a copy button
// ---------------------------------------------------------------------------

function CopyableCode({ code, language }: { code: string; language: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* noop */
    }
  };

  return (
    <div className="group relative overflow-hidden rounded-lg bg-slate-950 dark:bg-black/40">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2">
        <span className="text-xs font-semibold text-slate-400">{language}</span>
        <button
          type="button"
          onClick={handleCopy}
          className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium text-slate-400 transition-colors hover:bg-white/10 hover:text-white"
          aria-label="Copy code"
        >
          {copied ? <Check className="size-3.5 text-emerald-400" /> : <Copy className="size-3.5" />}
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <pre className="overflow-x-auto p-4 text-sm leading-relaxed text-slate-200">
        <code>{code}</code>
      </pre>
    </div>
  );
}

// ---------------------------------------------------------------------------
// OS tabs
// ---------------------------------------------------------------------------

const OS_TABS: { id: ShellOS; label: string; icon: typeof Apple }[] = [
  { id: 'unix', label: 'macOS / Linux', icon: Apple },
  { id: 'cmd', label: 'CMD', icon: SquareTerminal },
  { id: 'powershell', label: 'PowerShell', icon: Monitor },
];

// ---------------------------------------------------------------------------
// Snippet generators — each function returns CodeSnippet[] for a tab
// ---------------------------------------------------------------------------

function curlSnippets(baseUrl: string): CodeSnippet[] {
  const v1 = baseUrl.replace(/\/+$/, '');
  return [
    {
      label: '列出模型',
      language: 'bash',
      code: `curl ${v1}/v1/models \\
  -H "Authorization: Bearer $API_KEY"`,
    },
    {
      label: '非流式对话',
      language: 'bash',
      code: `curl -X POST ${v1}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer $API_KEY" \\
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "你好"}]
  }'`,
    },
    {
      label: '流式对话',
      language: 'bash',
      code: `curl -X POST ${v1}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer $API_KEY" \\
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "讲个故事"}],
    "stream": true
  }'`,
    },
  ];
}

function pythonSnippets(baseUrl: string): CodeSnippet[] {
  const v1 = `${baseUrl.replace(/\/+$/, '')}/v1`;
  return [
    {
      label: '安装 SDK',
      language: 'bash',
      code: `pip install openai`,
    },
    {
      label: '非流式',
      language: 'python',
      code: `from openai import OpenAI

client = OpenAI(
    api_key="<YOUR_API_KEY>",
    base_url="${v1}",
)

response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "你好"}],
)
print(response.choices[0].message.content)`,
    },
    {
      label: '流式',
      language: 'python',
      code: `stream = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "讲个故事"}],
    stream=True,
)
for chunk in stream:
    delta = chunk.choices[0].delta.content
    if delta:
        print(delta, end="", flush=True)`,
    },
  ];
}

function nodejsSnippets(baseUrl: string): CodeSnippet[] {
  const v1 = `${baseUrl.replace(/\/+$/, '')}/v1`;
  return [
    {
      label: '安装 SDK',
      language: 'bash',
      code: `npm install openai`,
    },
    {
      label: '非流式',
      language: 'typescript',
      code: `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "<YOUR_API_KEY>",
  baseURL: "${v1}",
});

const response = await client.chat.completions.create({
  model: "gpt-4o-mini",
  messages: [{ role: "user", content: "你好" }],
});
console.log(response.choices[0].message.content);`,
    },
  ];
}

function claudeCodeSnippets(baseUrl: string, os: ShellOS): CodeSnippet[] {
  const clean = baseUrl.replace(/\/+$/, '');
  let shellCode: string;
  let shellLabel: string;

  switch (os) {
    case 'cmd':
      shellLabel = 'CMD';
      shellCode = `set ANTHROPIC_BASE_URL=${clean}
set ANTHROPIC_AUTH_TOKEN=<YOUR_API_KEY>
set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
set CLAUDE_CODE_ATTRIBUTION_HEADER=0`;
      break;
    case 'powershell':
      shellLabel = 'PowerShell';
      shellCode = `$env:ANTHROPIC_BASE_URL = "${clean}"
$env:ANTHROPIC_AUTH_TOKEN = "<YOUR_API_KEY>"
$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
$env:CLAUDE_CODE_ATTRIBUTION_HEADER = "0"`;
      break;
    default:
      shellLabel = 'Terminal';
      shellCode = `export ANTHROPIC_BASE_URL="${clean}"
export ANTHROPIC_AUTH_TOKEN="<YOUR_API_KEY>"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export CLAUDE_CODE_ATTRIBUTION_HEADER=0`;
  }

  const settingsPath =
    os === 'unix'
      ? '~/.claude/settings.json'
      : '%USERPROFILE%\\.claude\\settings.json';

  const settingsJson = `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "env": {
    "ANTHROPIC_BASE_URL": "${clean}",
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0"
  }
}`;

  return [
    {
      label: '方式一：环境变量（推荐）',
      language: shellLabel,
      code: shellCode,
      notes: [
        '将环境变量加入 Shell 配置（~/.zshrc / ~/.bashrc）可实现持久化。',
        'ANTHROPIC_BASE_URL 不要带 /v1 后缀，SDK 会自动拼接路径。',
      ],
    },
    {
      label: '方式二：settings.json',
      language: settingsPath,
      code: settingsJson,
      notes: [
        '写入 ~/.claude/settings.json 后重启 Claude Code 即可生效。',
        '本平台提供 /v1/messages（Anthropic Messages API）兼容端点。',
      ],
    },
    {
      label: '验证连通',
      language: 'bash',
      code: `claude --print "你好"`,
    },
  ];
}

function codexSnippets(baseUrl: string, os: ShellOS): CodeSnippet[] {
  const clean = baseUrl.replace(/\/+$/, '');
  const v1 = `${clean}/v1`;
  let envCode: string;
  let envLabel: string;

  switch (os) {
    case 'cmd':
      envLabel = 'CMD';
      envCode = `set OPENAI_API_KEY=<YOUR_API_KEY>
set OPENAI_API_BASE=${v1}`;
      break;
    case 'powershell':
      envLabel = 'PowerShell';
      envCode = `$env:OPENAI_API_KEY = "<YOUR_API_KEY>"
$env:OPENAI_API_BASE = "${v1}"`;
      break;
    default:
      envLabel = 'Terminal';
      envCode = `export OPENAI_API_KEY="<YOUR_API_KEY>"
export OPENAI_API_BASE="${v1}"`;
  }

  return [
    {
      label: '环境变量',
      language: envLabel,
      code: envCode,
    },
    {
      label: 'config.toml（可选）',
      language: '~/.codex/config.toml',
      code: `[api]
base_url = "${v1}"
api_key = "<YOUR_API_KEY>"`,
    },
    {
      label: '验证',
      language: 'bash',
      code: `curl ${v1}/models \\
  -H "Authorization: Bearer $OPENAI_API_KEY"`,
    },
  ];
}

function geminiSnippets(baseUrl: string, os: ShellOS): CodeSnippet[] {
  const clean = baseUrl.replace(/\/+$/, '');
  let envCode: string;
  let envLabel: string;

  switch (os) {
    case 'cmd':
      envLabel = 'CMD';
      envCode = `set GEMINI_API_KEY=<YOUR_API_KEY>
set GOOGLE_GEMINI_BASE_URL=${clean}`;
      break;
    case 'powershell':
      envLabel = 'PowerShell';
      envCode = `$env:GEMINI_API_KEY = "<YOUR_API_KEY>"
$env:GOOGLE_GEMINI_BASE_URL = "${clean}"`;
      break;
    default:
      envLabel = 'Terminal';
      envCode = `export GEMINI_API_KEY="<YOUR_API_KEY>"
export GOOGLE_GEMINI_BASE_URL="${clean}"`;
  }

  return [
    {
      label: '环境变量',
      language: envLabel,
      code: envCode,
      notes: ['需管理员在渠道中配置 Gemini provider 适配器后使用。'],
    },
  ];
}

// ---------------------------------------------------------------------------
// Client tab definitions
// ---------------------------------------------------------------------------

interface ClientTab {
  id: string;
  label: string;
  icon: typeof Terminal;
  hasOS: boolean;
  generate: (baseUrl: string, os: ShellOS) => CodeSnippet[];
}

const CLIENT_TABS: ClientTab[] = [
  { id: 'curl', label: 'cURL', icon: Terminal, hasOS: false, generate: (b) => curlSnippets(b) },
  { id: 'python', label: 'Python', icon: Code2, hasOS: false, generate: (b) => pythonSnippets(b) },
  { id: 'nodejs', label: 'Node.js', icon: Code2, hasOS: false, generate: (b) => nodejsSnippets(b) },
  { id: 'claude-code', label: 'Claude Code', icon: Bot, hasOS: true, generate: (b, os) => claudeCodeSnippets(b, os) },
  { id: 'codex', label: 'Codex', icon: Bot, hasOS: true, generate: (b, os) => codexSnippets(b, os) },
  { id: 'gemini', label: 'Gemini CLI', icon: Bot, hasOS: true, generate: (b, os) => geminiSnippets(b, os) },
];

// ---------------------------------------------------------------------------
// Endpoint reference data
// ---------------------------------------------------------------------------

const endpointList = [
  { method: 'POST', path: '/v1/chat/completions', desc: 'OpenAI Chat Completions（流式 / 非流式）' },
  { method: 'POST', path: '/v1/completions', desc: 'OpenAI Completions（文本补全）' },
  { method: 'POST', path: '/v1/responses', desc: 'OpenAI Responses API' },
  { method: 'POST', path: '/v1/messages', desc: 'Anthropic Messages API（Claude 兼容）' },
  { method: 'GET', path: '/v1/models', desc: '列出可用模型' },
  { method: 'GET', path: '/v1/models/{model}', desc: '获取单个模型详情' },
  { method: 'POST', path: '/v1/embeddings', desc: '文本向量化' },
  { method: 'POST', path: '/v1/images/generations', desc: '图像生成' },
  { method: 'POST', path: '/v1/audio/transcriptions', desc: '语音转文字' },
  { method: 'POST', path: '/v1/audio/speech', desc: '文字转语音' },
  { method: 'POST', path: '/v1/moderations', desc: '内容审核' },
  { method: 'GET', path: '/v1/subscription/usage', desc: '订阅套餐用量查询' },
];

const methodColors: Record<string, string> = {
  GET: 'bg-blue-100 text-blue-700 dark:bg-blue-500/15 dark:text-blue-300',
  POST: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
};

// ---------------------------------------------------------------------------
// Step card component for the quick-start section
// ---------------------------------------------------------------------------

interface StepCardProps {
  index: number;
  icon: typeof KeyRound;
  title: string;
  children: React.ReactNode;
}

function StepCard({ index, icon: Icon, title, children }: StepCardProps) {
  return (
    <div className="flex gap-4">
      <div className="flex flex-col items-center">
        <div className="grid size-10 shrink-0 place-items-center rounded-xl bg-blue-600 text-white shadow-sm shadow-blue-600/20">
          <Icon className="size-5" />
        </div>
        {index < 3 && <div className="mt-1 w-px flex-1 bg-slate-200 dark:bg-white/10" />}
      </div>
      <div className="min-w-0 flex-1 pb-6">
        <p className="mb-1 text-xs font-bold uppercase tracking-wide text-blue-600 dark:text-blue-400">
          步骤 {String(index).padStart(2, '0')}
        </p>
        <h4 className="text-sm font-bold text-slate-900 dark:text-white">{title}</h4>
        <div className="mt-1 text-sm text-slate-600 dark:text-slate-400">{children}</div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Inline code badge
// ---------------------------------------------------------------------------

function CodeBadge({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-slate-100 px-1.5 py-0.5 text-xs font-mono text-blue-600 dark:bg-white/10 dark:text-blue-400">
      {children}
    </code>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export function ApiGuidePage() {
  const [baseUrl, setBaseUrl] = useState(window.location.origin);
  const [activeTab, setActiveTab] = useState('curl');
  const [activeOS, setActiveOS] = useState<ShellOS>('unix');

  useEffect(() => {
    let cancelled = false;
    resolveRelayBaseUrl()
      .then((resolved) => {
        if (!cancelled) setBaseUrl(resolved.url);
      })
      .catch(() => {
        // Keep the same-origin snippet fallback if status/build configuration is invalid.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const currentTab = CLIENT_TABS.find((t) => t.id === activeTab) ?? CLIENT_TABS[0];
  const snippets = currentTab.generate(baseUrl, activeOS);

  // -----------------------------------------------------------------------
  // CC Switch deeplink builder
  // -----------------------------------------------------------------------

  const buildCCSwitchLink = useCallback(
    (app: 'claude' | 'codex' | 'gemini') => {
      const endpoint = app === 'codex' ? `${baseUrl}/v1` : baseUrl;
      const params = new URLSearchParams({
        resource: 'provider',
        app,
        name: 'Micro-One API',
        endpoint,
        apiKey: '<YOUR_API_KEY>',
        homepage: baseUrl,
        enabled: 'true',
      });
      return `ccswitch://v1/import?${params.toString()}`;
    },
    [baseUrl],
  );

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      {/* Header */}
      <div className="space-y-1">
        <h2 className="text-2xl font-bold tracking-tight text-slate-950 dark:text-white">
          API 使用指南
        </h2>
        <p className="text-sm text-slate-500 dark:text-slate-400">
          创建 API 密钥后，参照以下示例接入各类客户端和 CLI 工具。
        </p>
      </div>

      {/* Connection info banner */}
      <div className="flex flex-wrap items-center gap-3 rounded-xl bg-gradient-to-r from-blue-50 to-indigo-50 p-4 dark:from-blue-500/10 dark:to-indigo-500/10">
        <Globe className="size-5 shrink-0 text-blue-600 dark:text-blue-400" />
        <div className="min-w-0 flex-1">
          <p className="text-xs font-semibold uppercase tracking-wide text-blue-600 dark:text-blue-400">
            API 地址
          </p>
          <input
            type="text"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://your-api-domain.com"
            className="mt-0.5 w-full rounded-md border border-blue-200/50 bg-white/50 px-2 py-1 font-mono text-sm font-semibold text-slate-900 outline-none transition-colors focus:border-blue-500 focus:bg-white dark:border-white/10 dark:bg-white/5 dark:text-white"
          />
        </div>
        <div className="shrink-0 rounded-lg bg-white/60 px-3 py-2 dark:bg-white/5">
          <p className="text-xs font-semibold uppercase tracking-wide text-slate-400">鉴权</p>
          <p className="font-mono text-sm font-semibold text-slate-900 dark:text-white">
            Bearer &lt;key&gt;
          </p>
        </div>
      </div>

      {/* Quick start */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-bold">快速开始</CardTitle>
          <CardDescription>三步完成首次 API 调用。</CardDescription>
        </CardHeader>
        <CardContent>
          <StepCard index={1} icon={KeyRound} title="创建 API 密钥">
            进入 <strong className="font-semibold text-slate-900 dark:text-white">API 密钥</strong>{' '}
            页面，点击「Create Token」。新密钥只会完整显示一次，请立即复制。
          </StepCard>
          <StepCard index={2} icon={Globe} title="确认 API 地址">
            上方显示的就是你的 API 服务地址。下方所有代码示例会自动使用此地址。
          </StepCard>
          <StepCard index={3} icon={Terminal} title="选择客户端接入">
            在下方选择对应的客户端标签，复制代码并将 <CodeBadge>{'<YOUR_API_KEY>'}</CodeBadge>{' '}
            替换为你的密钥。
          </StepCard>
        </CardContent>
      </Card>

      {/* Client code examples */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base font-bold">客户端接入示例</CardTitle>
          <CardDescription>选择客户端查看对应的代码示例。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* Client tabs */}
          <div className="flex flex-wrap gap-2">
            {CLIENT_TABS.map((tab) => {
              const Icon = tab.icon;
              const isActive = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  onClick={() => setActiveTab(tab.id)}
                  className={cn(
                    'flex items-center gap-2 rounded-lg border px-3 py-2 text-sm font-semibold transition-colors',
                    isActive
                      ? 'border-blue-500 bg-blue-50 text-blue-600 dark:border-blue-400 dark:bg-blue-500/10 dark:text-blue-300'
                      : 'border-slate-200 text-slate-500 hover:bg-slate-50 dark:border-white/10 dark:text-slate-400 dark:hover:bg-white/5',
                  )}
                >
                  <Icon className="size-4" />
                  {tab.label}
                </button>
              );
            })}
          </div>

          {/* OS tabs (for clients that need them) */}
          {currentTab.hasOS && (
            <div className="flex gap-2">
              {OS_TABS.map((tab) => {
                const Icon = tab.icon;
                const isActive = activeOS === tab.id;
                return (
                  <button
                    key={tab.id}
                    type="button"
                    onClick={() => setActiveOS(tab.id)}
                    className={cn(
                      'flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-semibold transition-colors',
                      isActive
                        ? 'bg-slate-900 text-white dark:bg-white dark:text-slate-900'
                        : 'bg-slate-100 text-slate-500 hover:bg-slate-200 dark:bg-white/5 dark:text-slate-400 dark:hover:bg-white/10',
                    )}
                  >
                    <Icon className="size-3.5" />
                    {tab.label}
                  </button>
                );
              })}
            </div>
          )}

          {/* Snippets */}
          <div className="space-y-4">
            {snippets.map((snippet, i) => (
              <div key={i} className="space-y-1.5">
                <p className="text-xs font-bold text-slate-600 dark:text-slate-300">
                  {snippet.label}
                </p>
                <CopyableCode code={snippet.code} language={snippet.language} />
                {snippet.notes && snippet.notes.length > 0 && (
                  <ul className="space-y-1 pt-0.5">
                    {snippet.notes.map((note, ni) => (
                      <li
                        key={ni}
                        className="flex gap-2 text-xs text-slate-400 dark:text-slate-500"
                      >
                        <ArrowRight className="mt-0.5 size-3 shrink-0 text-slate-300 dark:text-slate-600" />
                        <span>{note}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* CC Switch */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-bold">
            CC Switch 一键导入
          </CardTitle>
          <CardDescription>
            已安装 CC Switch 的用户可通过深度链接快速导入配置。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-slate-500 dark:text-slate-400">
            点击下方按钮打开 CC Switch 导入窗口，导入前请将{' '}
            <CodeBadge>{'<YOUR_API_KEY>'}</CodeBadge> 替换为实际密钥。
          </p>
          <div className="grid gap-2 sm:grid-cols-3">
            {[
              { app: 'claude' as const, label: 'Claude Code' },
              { app: 'codex' as const, label: 'Codex' },
              { app: 'gemini' as const, label: 'Gemini CLI' },
            ].map(({ app, label }) => (
              <div key={app} className="space-y-1">
                <p className="text-xs font-semibold text-slate-500 dark:text-slate-400">
                  {label}
                </p>
                <div className="overflow-hidden rounded-lg bg-slate-950 dark:bg-black/40">
                  <div className="flex items-center justify-between border-b border-white/10 px-3 py-1.5">
                    <span className="text-xs font-semibold text-slate-400">深度链接</span>
                    <button
                      type="button"
                      onClick={() => window.open(buildCCSwitchLink(app), '_blank')}
                      className="flex items-center gap-1 rounded-md bg-blue-600 px-2 py-1 text-xs font-medium text-white transition-colors hover:bg-blue-700"
                    >
                      <ArrowRight className="size-3" />
                      打开
                    </button>
                  </div>
                  <pre className="overflow-x-auto p-3 text-xs leading-relaxed text-slate-300">
                    <code>{buildCCSwitchLink(app)}</code>
                  </pre>
                </div>
              </div>
            ))}
          </div>
          <div className="rounded-lg bg-blue-50 p-3 dark:bg-blue-500/10">
            <p className="text-xs text-blue-700 dark:text-blue-300">
              💡 也可以在「API 密钥」页面直接点击某个密钥的「CC Switch」按钮，系统会自动填入该密钥，无需手动替换。
            </p>
          </div>
        </CardContent>
      </Card>

      {/* Endpoint reference */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-bold">
            <FileText className="size-4 text-slate-400" />
            API 端点参考
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-white/10">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-slate-200 bg-slate-50 dark:border-white/10 dark:bg-white/5">
                  <th className="px-4 py-3 text-left text-xs font-bold uppercase tracking-wide text-slate-400">
                    方法
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-bold uppercase tracking-wide text-slate-400">
                    路径
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-bold uppercase tracking-wide text-slate-400">
                    说明
                  </th>
                </tr>
              </thead>
              <tbody>
                {endpointList.map((endpoint) => (
                  <tr
                    key={endpoint.path}
                    className="border-b border-slate-100 last:border-0 dark:border-white/5"
                  >
                    <td className="px-4 py-2.5">
                      <span
                        className={cn(
                          'inline-flex rounded-md px-2 py-0.5 text-xs font-bold',
                          methodColors[endpoint.method] || 'bg-slate-100 text-slate-600',
                        )}
                      >
                        {endpoint.method}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs font-semibold text-slate-900 dark:text-white">
                      {endpoint.path}
                    </td>
                    <td className="px-4 py-2.5 text-sm text-slate-500 dark:text-slate-400">
                      {endpoint.desc}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {/* Safety tips */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-bold">
            <ShieldCheck className="size-4 text-orange-500" />
            安全提示
          </CardTitle>
        </CardHeader>
        <CardContent>
          <ul className="space-y-2.5 text-sm text-slate-600 dark:text-slate-300">
            <li className="flex gap-2.5">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-orange-400" />
              API 密钥创建后只会完整显示一次，请立即复制并保存到安全的密钥管理工具中。
            </li>
            <li className="flex gap-2.5">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-orange-400" />
              切勿将 API 密钥写入代码仓库、聊天记录或公开文档。
            </li>
            <li className="flex gap-2.5">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-orange-400" />
              为不同用途创建独立命名的 Token，便于在「使用记录」中区分调用来源。
            </li>
            <li className="flex gap-2.5">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-orange-400" />
              不再使用的 Token 应及时删除，避免密钥泄露风险。
            </li>
          </ul>
        </CardContent>
      </Card>
    </div>
  );
}
