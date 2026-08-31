import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { PrivacyPolicyPage, UserAgreementPage } from './LegalPage';

function mockLegalStatus() {
  server.use(
    http.get('/api/status', () => HttpResponse.json({
      success: true,
      data: {
        system_name: 'Micro-One API',
        legal_operator_name: 'Example Operator',
        legal_operator_address: 'Example Address',
        legal_contact_email: 'privacy@example.com',
      },
    })),
  );
}

describe('legal page language synchronization', () => {
  it('switches the user agreement title and body together', async () => {
    mockLegalStatus();
    const user = userEvent.setup();

    renderWithQuery(<MemoryRouter><UserAgreementPage /></MemoryRouter>);

    expect(screen.getByRole('heading', { name: 'Micro-One API 用户协议' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '特别提示' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('heading', { name: 'Micro-One API User Agreement' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Important Notice' })).toBeInTheDocument();
    expect(document.body.textContent?.replaceAll('中文', '')).not.toMatch(/[\u3400-\u9fff]/);
  });

  it('switches the privacy policy title and table together', async () => {
    mockLegalStatus();
    const user = userEvent.setup();

    renderWithQuery(<MemoryRouter><PrivacyPolicyPage /></MemoryRouter>);

    expect(screen.getByRole('heading', { name: 'Micro-One API 隐私政策' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '信息种类' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('heading', { name: 'Micro-One API Privacy Policy' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Information Type' })).toBeInTheDocument();
    expect(document.body.textContent?.replaceAll('中文', '')).not.toMatch(/[\u3400-\u9fff]/);
  });
});

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

    renderWithQuery(<MemoryRouter><UserAgreementPage /></MemoryRouter>);

    expect(screen.getByRole('heading', { name: 'Micro-One API 用户协议' })).toBeInTheDocument();
    expect(await screen.findByText('示例科技有限公司')).toBeInTheDocument();
    expect(screen.getByText('上海市示例路 1 号')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'privacy@example.com' })).toHaveAttribute(
      'href',
      'mailto:privacy@example.com',
    );
    expect(screen.queryByText('运营信息尚未完整配置')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '十一、法律适用与争议解决' })).toBeInTheDocument();
  });

  it('discloses model-provider processing and cross-border requirements', async () => {
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: {} })),
    );

    renderWithQuery(<MemoryRouter><PrivacyPolicyPage /></MemoryRouter>);

    expect(screen.getByRole('heading', { name: 'Micro-One API 隐私政策' })).toBeInTheDocument();
    expect(await screen.findByText('运营信息尚未完整配置')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '四、个人信息跨境提供' })).toBeInTheDocument();
    expect(screen.getByText(/部分境外模型服务商/)).toBeInTheDocument();
  });
});
