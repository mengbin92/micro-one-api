import { http, HttpResponse } from 'msw';
import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { PrivacyPolicyPage, UserAgreementPage } from './LegalPage';
import { server } from '@/test/msw/server';
import { renderWithQuery } from '@/test/render';

describe('legal pages', () => {
  it('renders the configured operator identity in the user agreement', async () => {
    server.use(
      http.get('/api/status', () => HttpResponse.json({
        success: true,
        data: {
          system_name: 'Example API',
          legal_operator_name: '示例科技有限公司',
          legal_operator_address: '上海市示例路 1 号',
          legal_contact_email: 'privacy@example.com',
        },
      })),
    );

    renderWithQuery(
      <MemoryRouter>
        <UserAgreementPage />
      </MemoryRouter>,
    );

    expect(screen.getByRole('heading', { name: 'Micro-One API 用户协议' })).toBeInTheDocument();
    expect(await screen.findByText('示例科技有限公司')).toBeInTheDocument();
    expect(screen.queryByText('运营信息尚未完整配置')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '十一、法律适用与争议解决' })).toBeInTheDocument();
  });

  it('discloses model-provider processing and cross-border requirements', async () => {
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: {} })),
    );

    renderWithQuery(
      <MemoryRouter>
        <PrivacyPolicyPage />
      </MemoryRouter>,
    );

    expect(screen.getByRole('heading', { name: 'Micro-One API 隐私政策' })).toBeInTheDocument();
    expect(await screen.findByText('运营信息尚未完整配置')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '四、个人信息跨境提供' })).toBeInTheDocument();
    expect(screen.getByText(/部分境外模型服务商/)).toBeInTheDocument();
  });
});
