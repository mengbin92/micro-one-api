import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { ModelMultiSelect } from './ModelMultiSelect';

function ControlledModelMultiSelect() {
  const [value, setValue] = useState('');
  return (
    <>
      <ModelMultiSelect value={value} onChange={setValue} />
      <output aria-label="selected models">{value}</output>
    </>
  );
}

describe('ModelMultiSelect', () => {
  beforeEach(() => window.localStorage.setItem('web:language', JSON.stringify('en-US')));
  afterEach(() => window.localStorage.removeItem('web:language'));

  it('adds a provider-specific model that is absent from the registry', async () => {
    server.use(
      http.get('/api/admin/models', () => HttpResponse.json({ models: [], total: 0 })),
    );
    const user = userEvent.setup();
    renderWithQuery(<ControlledModelMultiSelect />);

    const search = screen.getByPlaceholderText('Search models...');
    await user.type(search, 'z-ai/glm-5.2');
    await user.click(screen.getByRole('button', { name: 'Add z-ai/glm-5.2' }));

    expect(screen.getByLabelText('selected models')).toHaveTextContent('z-ai/glm-5.2');
    expect(search).toHaveValue('');
  });

  it('adds a custom model with Enter', async () => {
    server.use(
      http.get('/api/admin/models', () => HttpResponse.json({ models: [], total: 0 })),
    );
    const user = userEvent.setup();
    renderWithQuery(<ControlledModelMultiSelect />);

    const search = screen.getByPlaceholderText('Search models...');
    await user.type(search, 'GLM-5.2{Enter}');

    expect(screen.getByLabelText('selected models')).toHaveTextContent('GLM-5.2');
  });
});
