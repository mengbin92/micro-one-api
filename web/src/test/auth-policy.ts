import type { RegistrationPolicy } from '@/hooks/useRegistrationPolicy';

export const defaultRegistrationPolicy: RegistrationPolicy = {
  registration_enabled: true,
  email_verification: false,
  email_domain_restriction_enabled: false,
  email_domain_whitelist: [],
  turnstile_check: false,
  turnstile_site_key: '',
  email_delivery_configured: true,
};
