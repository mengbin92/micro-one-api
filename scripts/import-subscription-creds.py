#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
import-subscription-creds.py — 从本地 Claude Code / Codex CLI 的 OAuth 凭证中
抽取 access_token / refresh_token / account_id / expires_at，生成可直接 POST 到
micro-one-api /v1/subscription-accounts 的 payload。

用法:
  python3 scripts/import-subscription-creds.py claude \
      --name claude-pro-1 --group default \
      --models "claude-sonnet-4,claude-opus-4" --priority 100 \
      [--admin-url http://127.0.0.1:8000 --admin-token <tok> --apply]

  python3 scripts/import-subscription-creds.py codex \
      --name chatgpt-pro-1 --group default \
      --models "gpt-5,gpt-5-codex,codex-mini-latest,o4-mini" --priority 90 \
      [--apply]

凭证来源（按优先级尝试，命中即止）:
  Claude (macOS):  Keychain service "Claude Code-credentials" (JSON blob)
  Claude (Linux):  ~/.claude/.credentials.json
  Codex (通用):    ~/.codex/auth.json  或  ~/.config/codex/auth.json

输出:
  - 默认: 写完整 payload (含明文 secret) 到 ./subscription-account.<platform>.payload.json
          并在 stdout 打印【脱敏】摘要 + 可直接复制的 curl 命令。
  - --apply: 直接 POST 到 --admin-url 的 /v1/subscription-accounts。

注意: 本脚本仅读取本机凭证用于导入到自建网关，不会外发任何数据。
"""
import argparse, base64, json, os, subprocess, sys, time, urllib.request

# ---------- 通用小工具 ----------
def redact(s):
    if not s or not isinstance(s, str):
        return s
    n = len(s)
    if n <= 12:
        return s[:2] + "***"
    return s[:6] + "…" + s[-4:]

def jwt_payload(token):
    """解码 JWT payload（不验签），用于取 exp/sub 等。非 JWT 返回 None。"""
    if not token or token.count(".") != 2:
        return None
    try:
        seg = token.split(".")[1]
        seg += "=" * (-len(seg) % 4)
        return json.loads(base64.urlsafe_b64decode(seg))
    except Exception:
        return None

def first_present(d, keys):
    for k in keys:
        v = d.get(k) if isinstance(d, dict) else None
        if v not in (None, "", 0):
            return v
    return None

# ---------- Claude 凭证 ----------
def macos_keychain_json(services):
    for svc in services:
        try:
            out = subprocess.run(
                ["security", "find-generic-password", "-s", svc, "-w"],
                capture_output=True, text=True, timeout=8,
            )
        except Exception:
            continue
        if out.returncode == 0 and out.stdout.strip():
            try:
                return json.loads(out.stdout.strip()), svc
            except Exception:
                # 部分旧版把 JSON 转义存储，尝试反转义
                try:
                    return json.loads(out.stdout.strip().encode().decode("unicode_escape")), svc
                except Exception:
                    continue
    return None, None

def load_claude():
    # 1) macOS Keychain
    blob, src = macos_keychain_json([
        "Claude Code-credentials",
        "Claude Code",
        "com.anthropic.claude.credentials",
    ])
    if blob is None:
        # 2) Linux 文件
        for p in [os.path.expanduser("~/.claude/.credentials.json"),
                  os.path.expanduser("~/.config/claude/.credentials.json")]:
            if os.path.exists(p):
                try:
                    blob = json.load(open(p)); src = p; break
                except Exception:
                    pass
    if not isinstance(blob, dict):
        return None, "未找到 Claude OAuth 凭证（Keychain/文件均无）。请先在本机用 Claude Code 登录订阅。"

    oauth = blob.get("claudeAiOauth") if isinstance(blob.get("claudeAiOauth"), dict) else blob
    access  = first_present(oauth, ["accessToken", "access_token"])
    refresh = first_present(oauth, ["refreshToken", "refresh_token"])
    # Claude 的 expiresAt 通常是毫秒级 epoch
    exp = first_present(oauth, ["expiresAt", "expires_at", "expireAt"])
    if exp:
        exp = int(exp)
        if exp > 10_000_000_000:  # ms -> s
            exp //= 1000
    # account_id: Claude Code 凭证里一般没有现成 UUID，尝试从 id_token/sub 取
    account_id = first_present(oauth, ["account_id", "accountId", "organizationId"])
    if not account_id:
        id_tok = first_present(oauth, ["id_token", "idToken"])
        if id_tok:
            pl = jwt_payload(id_tok)
            if pl:
                account_id = first_present(pl, ["sub", "account_id", "https://api.anthropic.com/account_id"])
    if not access or not refresh:
        return None, f"凭证文件结构不符（src={src}），未解析出 access/refresh token。原始键: {list(oauth.keys()) if isinstance(oauth,dict) else type(oauth)}"
    return {
        "access_token": access, "refresh_token": refresh,
        "expires_at": exp or 0, "account_id": account_id or "",
        "_src": src,
    }, None

# ---------- Codex 凭证 ----------
def load_codex():
    paths = [os.path.expanduser("~/.codex/auth.json"),
             os.path.expanduser("~/.config/codex/auth.json")]
    blob, src = None, None
    for p in paths:
        if os.path.exists(p):
            try:
                blob = json.load(open(p)); src = p; break
            except Exception:
                pass
    if blob is None:
        return None, "未找到 Codex 凭证文件 (~/.codex/auth.json)。请先用 codex 登录 ChatGPT 订阅。"

    # ChatGPT 登录态: {"tokens": {"access_token","refresh_token","account_id",...}}
    tok = blob.get("tokens") if isinstance(blob.get("tokens"), dict) else blob
    access  = first_present(tok, ["access_token", "accessToken"])
    refresh = first_present(tok, ["refresh_token", "refreshToken"])
    account_id = first_present(tok, ["account_id", "accountId", "chatgpt_account_id"])
    if not access and blob.get("OPENAI_API_KEY"):
        return None, "检测到 ~/.codex/auth.json 仅含 OPENAI_API_KEY（API Key 模式，非 ChatGPT 订阅登录），无 OAuth token 可导入。请用 `codex login` 登录订阅账号。"

    # expires: 优先显式字段，其次解 JWT exp
    exp = first_present(tok, ["expires_at", "expiresAt", "expireAt"])
    if exp:
        exp = int(exp)
        if exp > 10_000_000_000:
            exp //= 1000
    elif access:
        pl = jwt_payload(access)
        if pl and pl.get("exp"):
            exp = int(pl["exp"])
    if not access or not refresh:
        return None, f"凭证文件结构不符（src={src}）。原始键: {list(tok.keys()) if isinstance(tok,dict) else type(tok)}"
    return {
        "access_token": access, "refresh_token": refresh,
        "expires_at": exp or 0, "account_id": account_id or "",
        "_src": src,
    }, None

# ---------- payload 组装 ----------
def build_payload(platform, creds, name, group, models, priority):
    return {
        "name": name or f"{platform}-imported-{int(time.time())}",
        "platform": platform,
        "account_type": "oauth",
        "group": group or "default",
        "models": models or (",".join(default_models(platform))),
        "priority": int(priority or 0),
        "base_url": "",
        "access_token": creds["access_token"],
        "refresh_token": creds["refresh_token"],
        "expires_at": creds["expires_at"],
        "account_id": creds["account_id"],
        "fingerprint": "",
        "metadata": "",
    }

def default_models(platform):
    return {
        "claude": ["claude-sonnet-4", "claude-opus-4", "claude-haiku-4"],
        "codex":  ["gpt-5", "gpt-5-codex", "codex-mini-latest", "o4-mini"],
    }[platform]

# ---------- 主流程 ----------
def main():
    ap = argparse.ArgumentParser(description="从本地 CLI 凭证导入订阅号到 micro-one-api")
    ap.add_argument("platform", choices=["claude", "codex"])
    ap.add_argument("--name", help="订阅号名称（不填自动生成）")
    ap.add_argument("--group", default="default")
    ap.add_argument("--models", help='逗号分隔的客户端暴露模型名，不填用平台默认')
    ap.add_argument("--priority", type=int, default=0)
    ap.add_argument("--admin-url", default="http://127.0.0.1:8000")
    ap.add_argument("--admin-token", default=os.environ.get("ADMIN_TOKEN", ""))
    ap.add_argument("--apply", action="store_true", help="直接 POST 到 admin API（需 --admin-token）")
    ap.add_argument("--show", action="store_true", help="在终端打印明文 payload（默认脱敏）")
    args = ap.parse_args()

    if args.platform == "claude":
        creds, err = load_claude()
    else:
        creds, err = load_codex()
    if err:
        print("✗ " + err, file=sys.stderr)
        sys.exit(1)

    payload = build_payload(args.platform, creds, args.name, args.group, args.models, args.priority)

    # 写完整 payload 到文件（含明文 secret）
    out_file = f"subscription-account.{args.platform}.payload.json"
    with open(out_file, "w") as f:
        json.dump(payload, f, ensure_ascii=False, indent=2)
    print(f"✓ 凭证来源: {creds['_src']}")
    print(f"✓ 完整 payload 已写入: {out_file}")
    print("─" * 50)
    print("脱敏摘要:")
    if payload["expires_at"]:
        exp_str = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(payload["expires_at"]))
    else:
        exp_str = "(0，将触发首次刷新)"
    summary = {**payload,
        "access_token":  redact(payload["access_token"]),
        "refresh_token": redact(payload["refresh_token"]),
        "account_id":    (redact(payload["account_id"]) or "(空，建议手动补)"),
        "expires_at":    exp_str,
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))

    if args.show:
        print("─" * 50)
        print("明文 payload:")
        print(json.dumps(payload, ensure_ascii=False, indent=2))

    # 生成 curl 命令
    print("─" * 50)
    print("手动导入命令（用文件避免命令行泄露 secret）:")
    print(f'curl -X POST {args.admin_url}/v1/subscription-accounts \\')
    print(f'  -H "Authorization: Bearer $ADMIN_TOKEN" \\')
    print(f'  -H "Content-Type: application/json" \\')
    print(f'  -d @{out_file}')

    if not payload["account_id"]:
        print("⚠ account_id 为空：")
        if args.platform == "claude":
            print("  Claude 的 metadata.user_id 需要 account UUID。凭证文件未直接携带，")
            print("  请到 Anthropic Console 或登录回调里获取后手动补到 payload 的 account_id。")
        else:
            print("  Codex 后端要求 chatgpt-account-id 头。请确认 codex login 用的 ChatGPT 账号，")
            print("  从 ~/.codex/auth.json 的 tokens.account_id 取（若脚本未自动识别请检查字段名）。")

    if args.apply:
        if not args.admin_token:
            print("✗ --apply 需要 --admin-token 或 ADMIN_TOKEN 环境变量", file=sys.stderr)
            sys.exit(1)
        data = json.dumps(payload).encode()
        req = urllib.request.Request(
            f"{args.admin_url}/v1/subscription-accounts",
            data=data, method="POST",
            headers={"Content-Type": "application/json",
                     "Authorization": f"Bearer {args.admin_token}"},
        )
        try:
            with urllib.request.urlopen(req, timeout=15) as resp:
                body = resp.read().decode()
                print("─" * 50)
                print(f"✓ 导入成功 HTTP {resp.status}")
                print(body)
        except urllib.error.HTTPError as e:
            print("─" * 50)
            print(f"✗ 导入失败 HTTP {e.code}: {e.read().decode()}", file=sys.stderr)
            sys.exit(1)
        except Exception as e:
            print("─" * 50)
            print(f"✗ 请求异常: {e}", file=sys.stderr)
            sys.exit(1)

if __name__ == "__main__":
    main()
