import { useState, useEffect } from 'react';
import { Check, Copy, Terminal, Code2, Bot, FileText, Download, MousePointerClick, Zap, ShieldCheck } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { API_BASE_URL } from '@/lib/api';

interface CodeBlock {
  language: string;
  code: string;
}

interface ClientGuide {
  id: string;
  label: string;
  icon: typeof Terminal;
  description: string;
  blocks: CodeBlock[];
  notes?: string[];
}

const clientGuides: ClientGuide[] = [
  {
    id: 'curl',
    label: 'cURL',
    icon: Terminal,
    description: '最快速验证接口连通性的方式。',
    blocks: [
      {
        language: 'bash',
        code: `# 列出可用模型
curl https://your-api-domain.com/v1/models \\
  -H "Authorization: Bearer \${API_KEY}"`,
      },
      {
        language: 'bash',
        code: `# 非流式对话
curl -X POST https://your-api-domain.com/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer \${API_KEY}" \\
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "你好"}
    ]
  }'`,
      },
      {
        language: 'bash',
        code: `# 流式对话（SSE）
curl -X POST https://your-api-domain.com/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer \${API_KEY}" \\
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "讲个故事"}],
    "stream": true
  }'`,
      },
    ],
  },
  {
    id: 'python',
    label: 'Python',
    icon: Code2,
    description: '使用官方 openai Python SDK。',
    blocks: [
      {
        language: 'bash',
        code: `pip install openai`,
      },
      {
        language: 'python',
        code: `from openai import OpenAI

client = OpenAI(
    api_key="<YOUR_API_KEY>",
    base_url="https://your-api-domain.com/v1",
)

response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "你好"}],
)
print(response.choices[0].message.content)`,
      },
      {
        language: 'python',
        code: `# 流式输出
stream = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "讲个故事"}],
    stream=True,
)
for chunk in stream:
    delta = chunk.choices[0].delta.content
    if delta:
        print(delta, end="", flush=True)`,
      },
    ],
    notes: [
      'base_url 需包含 /v1 后缀，SDK 会在其后拼接 /chat/completions。',
      'API Key 在控制台「API 密钥」页面创建，创建后只会完整显示一次。',
    ],
  },
  {
    id: 'nodejs',
    label: 'Node.js',
    icon: Code2,
    description: '使用官方 openai Node.js SDK。',
    blocks: [
      {
        language: 'bash',
        code: `npm install openai`,
      },
      {
        language: 'typescript',
        code: `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "<YOUR_API_KEY>",
  baseURL: "https://your-api-domain.com/v1",
});

const response = await client.chat.completions.create({
  model: "gpt-4o-mini",
  messages: [{ role: "user", content: "你好" }],
});
console.log(response.choices[0].message.content);`,
      },
    ],
    notes: ['同时支持 ESM 和 CommonJS 导入方式。'],
  },
  {
    id: 'claude-code',
    label: 'Claude Code',
    icon: Bot,
    description: '将 Claude Code CLI 指向本平台 Anthropic Messages 端点。',
    blocks: [
      {
        language: 'bash',
        code: `# 设置环境变量（推荐）
export ANTHROPIC_BASE_URL="https://your-api-domain.com"
export ANTHROPIC_API_KEY="<YOUR_API_KEY>"`,
      },
      {
        language: 'bash',
        code: `# 或写入 ~/.claude/settings.json
{
  "apiBaseUrl": "https://your-api-domain.com",
  "apiKey": "<YOUR_API_KEY>"
}`,
      },
      {
        language: 'bash',
        code: `# 验证连通
claude --print "你好"`,
      },
    ],
    notes: [
      '本平台提供 /v1/messages（Anthropic Messages API）兼容端点，支持 x-api-key 和 Bearer 两种鉴权头。',
      'ANTHROPIC_BASE_URL 不要带 /v1 后缀，SDK 会自动拼接路径。',
      '支持的模型取决于管理员在渠道中配置的 Claude 系列模型映射。',
    ],
  },
  {
    id: 'codex',
    label: 'Codex / ChatGPT',
    icon: Bot,
    description: '将 OpenAI 官方客户端 / Codex 指向本平台。',
    blocks: [
      {
        language: 'bash',
        code: `# 设置环境变量
export OPENAI_API_KEY="<YOUR_API_KEY>"
export OPENAI_API_BASE="https://your-api-domain.com/v1"`,
      },
      {
        language: 'bash',
        code: `# 或写入 ~/.codex/config.toml
[api]
base_url = "https://your-api-domain.com/v1"
api_key = "<YOUR_API_KEY>"`,
      },
      {
        language: 'bash',
        code: `# 验证连通
curl https://your-api-domain.com/v1/models \\
  -H "Authorization: Bearer \${OPENAI_API_KEY}"`,
      },
    ],
    notes: [
      'OPENAI_API_BASE 需包含 /v1 后缀。',
      '本平台同时支持 /v1/chat/completions 和 /v1/responses 端点。',
    ],
  },
  {
    id: 'gemini',
    label: 'Gemini CLI',
    icon: Bot,
    description: '将 Gemini CLI 指向本平台 Gemini 兼容端点。',
    blocks: [
      {
        language: 'bash',
        code: `# 设置环境变量
export GEMINI_API_KEY="<YOUR_API_KEY>"
export GEMINI_BASE_URL="https://your-api-domain.com"`,
      },
      {
        language: 'bash',
        code: `# 或写入 ~/.gemini/.env
GEMINI_API_KEY=<YOUR_API_KEY>
GEMINI_BASE_URL=https://your-api-domain.com`,
      },
    ],
    notes: ['需管理员在渠道中配置 Gemini provider 适配器后使用。'],
  },
];

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

const ccSwitchFeatures = [
  { title: '一键切换 API 配置', description: '在多个 API 提供商之间快速切换，减少反复改配置的成本。' },
  { title: '可视化配置管理', description: '通过图形界面直接检查现有配置，不需要在不同文件里来回查找。' },
  { title: 'MCP 服务多端管理', description: '集中管理 Model Context Protocol 服务配置，适合多端统一维护。' },
  { title: '系统托盘快捷操作', description: '可以通过托盘菜单快速切换，减少频繁打开完整窗口。' },
  { title: '本地代理与负载切换', description: '支持按场景切换不同上游，也支持自动故障转移。' },
  { title: '自动故障转移', description: '渠道异常时可自动切换到可用配置，降低中断风险。' },
];

const ccSwitchSteps = [
  {
    index: '01',
    title: '创建 API 密钥',
    description: '在控制台「API 密钥」页面点击 Create Token，创建一个新密钥并立即复制。新密钥只会完整显示一次。',
  },
  {
    index: '02',
    title: '下载并安装 CC Switch',
    description: '从 GitHub Releases 下载最新版 CC-Switch（支持 Windows / macOS / Linux），完成本地安装后打开主界面。',
  },
  {
    index: '03',
    title: '添加自定义供应商',
    description: '在 CC-Switch 中选择目标应用（Claude Code / Codex / Gemini），点击「+」添加供应商，选择「自定义」预设，填入名称、API Key 和端点地址。',
  },
  {
    index: '04',
    title: '使用深度链接一键导入（可选）',
    description: '也可用下方生成的 ccswitch:// 深度链接，点击后 CC-Switch 会自动打开并预填配置，确认即可导入。',
  },
  {
    index: '05',
    title: '切换到该供应商',
    description: '回到 CC-Switch 主界面，点击刚添加的供应商卡片将其设为当前渠道，确保状态指示为已选中。',
  },
  {
    index: '06',
    title: '终端验证',
    description: '打开终端，运行目标 CLI（codex / claude / gemini）进行一次简单对话，能正常进入会话并收到回复即说明配置生效。',
  },
];

function CopyableCode({ code, language }: { code: string; language: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // noop
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

function DeepLinkImporter({ baseUrl }: { baseUrl: string }) {
  const apiKeyPlaceholder = '<YOUR_API_KEY>';

  const buildLink = (app: string) => {
    const endpoint = app === 'codex' ? `${baseUrl}/v1` : baseUrl;
    const params = new URLSearchParams({
      resource: 'provider',
      app,
      name: 'Micro-One API',
      endpoint,
      apiKey: apiKeyPlaceholder,
    });
    return `ccswitch://v1/import?${params.toString()}`;
  };

  const links = [
    { app: 'claude', label: 'Claude Code' },
    { app: 'codex', label: 'Codex' },
    { app: 'gemini', label: 'Gemini CLI' },
  ];

  return (
    <div className="space-y-3">
      <p className="text-sm font-medium text-slate-600 dark:text-slate-300">
        点击下方按钮可一键将本平台配置导入 CC-Switch（需先安装 CC-Switch 并完成协议注册）。
        导入前请将 <code className="rounded bg-slate-100 px-1.5 py-0.5 text-xs font-mono text-blue-600 dark:bg-white/10 dark:text-blue-400">{apiKeyPlaceholder}</code> 替换为你的实际密钥。
      </p>
      <div className="flex flex-wrap gap-3">
        {links.map((item) => (
          <a
            key={item.app}
            href={buildLink(item.app)}
            className="inline-flex h-10 items-center gap-2 rounded-xl bg-orange-500 px-4 text-sm font-semibold text-white transition-colors hover:bg-orange-600"
          >
            <MousePointerClick className="size-4" />
            导入到 {item.label}
          </a>
        ))}
      </div>
      <div className="space-y-2">
        {links.map((item) => (
          <CopyableCode key={item.app} language="深度链接" code={buildLink(item.app)} />
        ))}
      </div>
      <p className="text-xs text-slate-400">
        深度链接中包含占位密钥，请勿直接分享含真实 API Key 的链接。安全地通过私密渠道传输配置。
      </p>
    </div>
  );
}

export function ApiGuidePage() {
  const [activeTab, setActiveTab] = useState(clientGuides[0].id);
  const activeGuide = clientGuides.find((g) => g.id === activeTab) ?? clientGuides[0];
  const [baseUrl, setBaseUrl] = useState(window.location.origin);

  useEffect(() => {
    let cancelled = false;
    fetch(`${API_BASE_URL}/status`)
      .then((res) => res.json())
      .then((json) => {
        if (cancelled) return;
        const addr = json?.data?.server_address;
        if (addr && typeof addr === 'string' && addr.trim()) {
          setBaseUrl(addr.replace(/\/+$/, ''));
        }
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, []);

  return (
    <div className="space-y-6">
      {/* Title */}
      <div>
        <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">API 使用指南</h2>
        <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">
          在控制台创建 API 密钥后，参照以下示例接入各类客户端。
        </p>
      </div>

      {/* Quick Start Card */}
      <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileText className="size-5 text-blue-600" />
            快速开始
          </CardTitle>
          <CardDescription>三步完成首次 API 调用。</CardDescription>
        </CardHeader>
        <CardContent>
          <ol className="space-y-3">
            <li className="flex gap-3">
              <span className="grid size-6 shrink-0 place-items-center rounded-full bg-blue-600 text-xs font-bold text-white">1</span>
              <div className="min-w-0 pt-0.5 text-sm text-slate-600 dark:text-slate-300">
                进入 <strong className="font-semibold text-slate-900 dark:text-white">API 密钥</strong> 页面，点击「Create Token」创建密钥。
                新密钥只会完整显示一次，请立即复制并安全保存。
              </div>
            </li>
            <li className="flex gap-3">
              <span className="grid size-6 shrink-0 place-items-center rounded-full bg-blue-600 text-xs font-bold text-white">2</span>
              <div className="min-w-0 pt-0.5 text-sm text-slate-600 dark:text-slate-300">
                「连接信息」显示的就是 API 服务部署地址： <code className="break-all rounded bg-slate-100 px-1.5 py-0.5 text-xs font-mono text-blue-600 dark:bg-white/10 dark:text-blue-400">{baseUrl}</code>。
              </div>
            </li>
            <li className="flex gap-3">
              <span className="grid size-6 shrink-0 place-items-center rounded-full bg-blue-600 text-xs font-bold text-white">3</span>
              <div className="min-w-0 pt-0.5 text-sm text-slate-600 dark:text-slate-300">
                选择对应的客户端标签，复制代码示例并将 <code className="rounded bg-slate-100 px-1.5 py-0.5 text-xs font-mono text-blue-600 dark:bg-white/10 dark:text-blue-400">{'<YOUR_API_KEY>'}</code> 替换为你的密钥。
              </div>
            </li>
          </ol>
        </CardContent>
      </Card>

      {/* Base URL & Auth */}
      <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader>
          <CardTitle>连接信息</CardTitle>
          <CardDescription>Base URL 与鉴权方式说明。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="rounded-lg border border-slate-200 p-4 dark:border-white/10">
              <p className="text-xs font-bold uppercase tracking-wide text-slate-400">Base URL</p>
              <input
                type="text"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="https://your-api-domain.com"
                className="mt-1 w-full rounded-md border border-slate-200 bg-transparent px-2 py-1 font-mono text-sm font-semibold text-slate-900 outline-none focus:border-blue-500 dark:border-white/10 dark:text-white"
              />
              <p className="mt-1 text-xs text-slate-400">
                可手动修改为实际部署地址，下方示例和深度链接会同步更新
              </p>
            </div>
            <div className="rounded-lg border border-slate-200 p-4 dark:border-white/10">
              <p className="text-xs font-bold uppercase tracking-wide text-slate-400">鉴权方式</p>
              <p className="mt-1 font-mono text-sm font-semibold text-slate-900 dark:text-white">
                Authorization: Bearer &lt;key&gt;
              </p>
              <p className="mt-1 text-xs text-slate-400">
                Anthropic 端点也支持 x-api-key 头
              </p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* CC Switch Guide */}
      <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Zap className="size-5 text-orange-500" />
            使用 CC-Switch 配置（推荐）
          </CardTitle>
          <CardDescription>通过图形界面管理 Claude Code / Codex / Gemini 的渠道切换，无需手动编辑配置文件。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {/* What is CC-Switch */}
          <div>
            <h3 className="text-sm font-bold text-slate-900 dark:text-white">什么是 CC-Switch？</h3>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              CC-Switch 是一个开源的桌面端管理工具，支持 Claude Code、Codex、Gemini CLI、OpenCode、OpenClaw 和 Hermes 的供应商配置管理。
            </p>
            <div className="mt-4 grid grid-cols-1 gap-3 xl:grid-cols-2">
              {ccSwitchFeatures.map((feature) => (
                <div key={feature.title} className="rounded-xl border border-slate-100 bg-white px-4 py-4 dark:border-white/10 dark:bg-card">
                  <p className="text-sm font-semibold text-slate-900 dark:text-white">{feature.title}</p>
                  <p className="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">{feature.description}</p>
                </div>
              ))}
            </div>
          </div>

          {/* Download */}
          <div className="rounded-xl border border-slate-100 bg-white p-4 dark:border-white/10 dark:bg-card">
            <p className="text-sm font-semibold text-slate-900 dark:text-white">下载 CC-Switch</p>
            <p className="mt-2 text-sm leading-7 text-slate-600 dark:text-slate-300">
              访问 GitHub Releases 页面下载最新版 CC-Switch，支持 Windows、macOS 和 Linux。
            </p>
            <a
              href="https://github.com/farion1231/cc-switch/releases"
              target="_blank"
              rel="noopener noreferrer"
              className="mt-4 inline-flex h-10 items-center gap-2 rounded-xl border border-orange-200 bg-orange-50 px-4 text-sm font-semibold text-orange-700 transition-colors hover:bg-orange-100 dark:border-orange-500/30 dark:bg-orange-500/10 dark:text-orange-300"
            >
              <Download className="size-4" />
              打开 CC-Switch 下载页
            </a>
          </div>

          {/* Config Steps */}
          <div>
            <h3 className="text-sm font-bold text-slate-900 dark:text-white">配置渠道（6 步）</h3>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              下面以通用流程为例，Claude Code、Codex 和 Gemini 的接入方式同理，只需在 CC-Switch 中选择目标应用。
            </p>
            <div className="mt-4 space-y-3">
              {ccSwitchSteps.map((step) => (
                <div key={step.index} className="rounded-xl border border-slate-100 bg-white px-4 py-4 dark:border-white/10 dark:bg-card">
                  <div className="flex items-start gap-3">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-orange-50 text-sm font-bold text-orange-600 dark:bg-orange-500/10 dark:text-orange-300">
                      {step.index}
                    </div>
                    <div className="min-w-0">
                      <p className="text-sm font-semibold text-slate-900 dark:text-white">{step.title}</p>
                      <p className="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">{step.description}</p>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* Deep Link Import */}
          <div>
            <h3 className="text-sm font-bold text-slate-900 dark:text-white">深度链接一键导入</h3>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              CC-Switch 支持 <code className="rounded bg-slate-100 px-1 py-0.5 text-xs font-mono text-blue-600 dark:bg-white/10 dark:text-blue-400">ccswitch://</code> 深度链接协议，点击链接即可自动打开 CC-Switch 并预填供应商配置。
            </p>
            <div className="mt-4">
              <DeepLinkImporter baseUrl={baseUrl} />
            </div>
          </div>

          {/* Terminal Verify */}
          <div>
            <h3 className="text-sm font-bold text-slate-900 dark:text-white">终端验证</h3>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              完成图形配置后，用最小命令确认线路已经可用。
            </p>
            <div className="mt-3 grid grid-cols-1 gap-4 xl:grid-cols-3">
              <div className="rounded-xl border border-slate-100 bg-white p-4 dark:border-white/10 dark:bg-card">
                <p className="text-sm font-semibold text-slate-900 dark:text-white">运行 codex</p>
                <p className="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400">如果你配置的是 Codex，打开终端运行 codex 并进行简单对话。</p>
                <div className="mt-3">
                  <CopyableCode language="BASH" code="codex" />
                </div>
              </div>
              <div className="rounded-xl border border-slate-100 bg-white p-4 dark:border-white/10 dark:bg-card">
                <p className="text-sm font-semibold text-slate-900 dark:text-white">运行 claude</p>
                <p className="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400">如果你配置的是 Claude Code，打开终端运行 claude 并进行简单对话。</p>
                <div className="mt-3">
                  <CopyableCode language="BASH" code="claude" />
                </div>
              </div>
              <div className="rounded-xl border border-slate-100 bg-white p-4 dark:border-white/10 dark:bg-card">
                <p className="text-sm font-semibold text-slate-900 dark:text-white">运行 gemini</p>
                <p className="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400">如果你配置的是 Gemini CLI，打开终端运行 gemini 并进行简单对话。</p>
                <div className="mt-3">
                  <CopyableCode language="BASH" code="gemini" />
                </div>
              </div>
            </div>
            <div className="mt-3 rounded-xl border border-slate-100 bg-white p-4 dark:border-white/10 dark:bg-card">
              <p className="text-sm font-semibold text-slate-900 dark:text-white">检查结果</p>
              <p className="mt-2 text-sm leading-7 text-slate-500 dark:text-slate-400">
                能正常进入会话并收到回复，就说明 CC-Switch 的配置已经生效。
              </p>
            </div>
          </div>

          {/* Supplementary notes */}
          <div className="rounded-lg bg-amber-50 p-4 dark:bg-amber-500/10">
            <p className="mb-2 text-xs font-bold text-amber-700 dark:text-amber-300">补充说明</p>
            <ul className="space-y-1.5">
              <li className="flex gap-2 text-sm text-amber-800 dark:text-amber-200">
                <span className="mt-1 size-1 shrink-0 rounded-full bg-amber-500" />
                <span>CC-Switch 供应商配置写入对应 CLI 的配置文件（如 ~/.claude/settings.json、~/.codex/config.toml、~/.gemini/.env），日常不需要手动创建额外配置文件。</span>
              </li>
              <li className="flex gap-2 text-sm text-amber-800 dark:text-amber-200">
                <span className="mt-1 size-1 shrink-0 rounded-full bg-amber-500" />
                <span>如果你配置的是 Claude Code，请确认当前机器已经完成过对应的 Claude Code 初次安装流程。</span>
              </li>
              <li className="flex gap-2 text-sm text-amber-800 dark:text-amber-200">
                <span className="mt-1 size-1 shrink-0 rounded-full bg-amber-500" />
                <span>Codex、Claude Code、Gemini 的流程基本一致，只需在 CC-Switch 顶部选择目标 CLI 即可。</span>
              </li>
            </ul>
          </div>
        </CardContent>
      </Card>

      {/* Client Tabs */}
      <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader>
          <CardTitle>客户端接入示例</CardTitle>
          <CardDescription>选择你使用的客户端或编程语言。</CardDescription>
        </CardHeader>
        <CardContent>
          {/* Tab Bar */}
          <div className="mb-6 flex items-center gap-1 overflow-x-auto border-b border-slate-200 pb-px dark:border-white/10">
            {clientGuides.map((guide) => {
              const Icon = guide.icon;
              const isActive = activeTab === guide.id;
              return (
                <button
                  key={guide.id}
                  type="button"
                  onClick={() => setActiveTab(guide.id)}
                  className={cn(
                    'flex shrink-0 items-center gap-2 border-b-2 px-4 py-2.5 text-sm font-semibold transition-colors',
                    isActive
                      ? 'border-blue-600 text-blue-600 dark:border-blue-400 dark:text-blue-400'
                      : 'border-transparent text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white',
                  )}
                >
                  <Icon className="size-4" />
                  {guide.label}
                </button>
              );
            })}
          </div>

          {/* Active guide content */}
          <div className="space-y-4">
            <p className="text-sm font-medium text-slate-500 dark:text-slate-400">
              {activeGuide.description}
            </p>

            {activeGuide.blocks.map((block, index) => (
              <CopyableCode key={`${activeGuide.id}-${index}`} code={block.code} language={block.language} />
            ))}

            {activeGuide.notes && activeGuide.notes.length > 0 && (
              <div className="rounded-lg bg-amber-50 p-4 dark:bg-amber-500/10">
                <p className="mb-2 text-xs font-bold text-amber-700 dark:text-amber-300">注意事项</p>
                <ul className="space-y-1.5">
                  {activeGuide.notes.map((note, index) => (
                    <li key={index} className="flex gap-2 text-sm text-amber-800 dark:text-amber-200">
                      <span className="mt-1 size-1 shrink-0 rounded-full bg-amber-500" />
                      <span>{note}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      {/* Endpoint Reference */}
      <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader>
          <CardTitle>API 端点参考</CardTitle>
          <CardDescription>本平台支持的 OpenAI 兼容端点。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-white/10">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-slate-200 bg-slate-50 dark:border-white/10 dark:bg-white/5">
                  <th className="px-4 py-3 text-left font-bold text-slate-600 dark:text-slate-300">方法</th>
                  <th className="px-4 py-3 text-left font-bold text-slate-600 dark:text-slate-300">路径</th>
                  <th className="px-4 py-3 text-left font-bold text-slate-600 dark:text-slate-300">说明</th>
                </tr>
              </thead>
              <tbody>
                {endpointList.map((endpoint) => (
                  <tr
                    key={endpoint.path}
                    className="border-b border-slate-100 last:border-0 dark:border-white/5"
                  >
                    <td className="px-4 py-3">
                      <span className={cn('inline-flex rounded-md px-2 py-1 text-xs font-bold', methodColors[endpoint.method] || 'bg-slate-100 text-slate-600')}>
                        {endpoint.method}
                      </span>
                    </td>
                    <td className="px-4 py-3 font-mono text-xs font-semibold text-slate-900 dark:text-white">
                      {endpoint.path}
                    </td>
                    <td className="px-4 py-3 text-slate-500 dark:text-slate-400">
                      {endpoint.desc}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {/* Safety Tips */}
      <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="size-5 text-orange-500" />
            安全提示
          </CardTitle>
        </CardHeader>
        <CardContent>
          <ul className="space-y-2 text-sm text-slate-600 dark:text-slate-300">
            <li className="flex gap-2">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-slate-300 dark:bg-slate-600" />
              API 密钥创建后只会完整显示一次，请立即复制并保存到安全的密钥管理工具中。
            </li>
            <li className="flex gap-2">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-slate-300 dark:bg-slate-600" />
              切勿将 API 密钥写入代码仓库、聊天记录或公开文档。
            </li>
            <li className="flex gap-2">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-slate-300 dark:bg-slate-600" />
              为不同用途创建独立命名的 Token，便于在「使用记录」中区分调用来源。
            </li>
            <li className="flex gap-2">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-slate-300 dark:bg-slate-600" />
              不再使用的 Token 应及时删除，避免密钥泄露风险。
            </li>
          </ul>
        </CardContent>
      </Card>
    </div>
  );
}
