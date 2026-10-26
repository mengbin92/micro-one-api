import { useQuery } from '@tanstack/react-query';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';

export interface RegistrationPolicy {
  registration_enabled: boolean;
  email_verification: boolean;
  email_domain_restriction_enabled: boolean;
  email_domain_whitelist: string[];
  turnstile_check: boolean;
  turnstile_site_key: string;
  email_delivery_configured: boolean;
  registration_policy_available?: boolean;
}

export function useRegistrationPolicy(enabled = true) {
  return useQuery({
    queryKey: ['public-registration-policy'],
    enabled,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await apiClient.get('/status', { signal });
      const policy = unwrapApiData<RegistrationPolicy>(response.data);
      if (!policy || policy.registration_policy_available === false || typeof policy.registration_enabled !== 'boolean'
        || typeof policy.email_verification !== 'boolean'
        || typeof policy.email_domain_restriction_enabled !== 'boolean'
        || !Array.isArray(policy.email_domain_whitelist)
        || !policy.email_domain_whitelist.every(domain => typeof domain === 'string')
        || typeof policy.turnstile_check !== 'boolean'
        || typeof policy.turnstile_site_key !== 'string'
        || typeof policy.email_delivery_configured !== 'boolean') {
        throw new Error('Invalid registration policy');
      }
      return policy;
    },
  });
}
