export function safePaymentURL(value: string): string | null {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password ? url.href : null;
  } catch { return null; }
}

export function safeWechatPaymentURL(value: string): string | null {
  try {
    const url = new URL(value);
    return value.length <= 2048 && url.protocol === 'weixin:' && url.hostname === 'wxpay'
      && ['/bizpayurl', '/bizpayurl/up'].includes(url.pathname) && !url.username && !url.password
      && Boolean(url.searchParams.get('pr')) ? value : null;
  } catch { return null; }
}
