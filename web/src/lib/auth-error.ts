import { getApiErrorMessage } from '@/lib/api-error';
import { t } from '@/lib/i18n';

const authErrors: Record<string, string> = {
  'email service unavailable': '邮件服务暂不可用，请联系管理员。',
  'too many requests': '请求过于频繁，请稍后重试。',
  'invalid or expired verification code': '验证码无效或已过期，请重新获取。',
  'invalid email': '邮箱格式无效',
  'email domain is not allowed': '该邮箱域名不在允许范围内',
  'email verification code is required': '请输入邮箱验证码',
  'registration disabled': '当前暂不开放注册。',
  'invalid parameter': '请检查输入内容',
  'turnstile verification is not configured': '安全验证尚未配置，请联系管理员。',
  'turnstile verification failed': '安全验证失败，请重试。',
  'user already exists': '用户名或邮箱已被使用',
};

export function getAuthErrorMessage(error: unknown, fallback: string) {
  const message = getApiErrorMessage(error, fallback);
  return t(authErrors[message] ?? message);
}
