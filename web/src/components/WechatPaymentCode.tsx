import { QRCodeSVG } from 'qrcode.react';
import { safeWechatPaymentURL } from '@/lib/payment-url';
import { t } from '@/lib/i18n';

export function WechatPaymentCode({ value }: { value: string }) {
  const url = safeWechatPaymentURL(value);
  if (!url) return <p role="alert">{t('支付地址无效')}</p>;
  return (
    <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-white p-4 text-center text-slate-900">
      <QRCodeSVG value={url} size={220} marginSize={4} title={t('微信支付二维码')} role="img" aria-label={t('微信支付二维码')} className="max-w-full" />
      <p className="text-sm">{t('请使用另一台设备上的微信扫一扫；本页仅支持 Native 扫码。')}</p>
      <p className="text-xs text-slate-600">{t('二维码到期后请创建新订单，支付结果以订单状态为准。')}</p>
    </div>
  );
}
