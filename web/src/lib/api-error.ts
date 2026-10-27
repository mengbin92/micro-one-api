import { t } from '@/lib/i18n';

const securityErrors: Record<string, string> = { MFA_INVALID: '验证码无效、过期或已使用', MFA_LOCKED: '尝试过多，请在五分钟后重试', MFA_UNAVAILABLE: '安全验证暂不可用', STEP_UP_REQUIRED: '请先完成安全验证', MFA_ENROLLMENT_REQUIRED: '请先在安全中心启用双重认证' };

export function getApiErrorMessage(error: unknown, fallback = 'Request failed') {
  if (typeof error === 'object' && error !== null) {
    const maybeError = error as {
      message?: unknown;
      response?: {
        data?: {
          message?: unknown;
          reason?: string;
          error?: unknown;
        };
      };
    };

    const securityReason = maybeError.response?.data?.reason;
    if (securityReason && securityErrors[securityReason]) return t(securityErrors[securityReason]);

    const responseMessage = maybeError.response?.data?.message;
    if (typeof responseMessage === 'string' && responseMessage.trim()) {
      return responseMessage;
    }

    const responseError = maybeError.response?.data?.error;
    if (typeof responseError === 'string' && responseError.trim()) {
      return responseError;
    }

    if (typeof maybeError.message === 'string' && maybeError.message.trim()) {
      return maybeError.message;
    }
  }

  return fallback;
}
