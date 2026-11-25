import type { ComponentProps } from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent, waitFor, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ThemeProvider } from '@/hooks/useTheme';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';

const NODE_INBOUND = {
  id: 1,
  port: 8080,
  protocol: 'vless',
  tag: 'n1-in-8080-tcp',
  remark: 'BS',
  nodeId: 1,
  enable: true,
} as unknown as InboundOption;

const LOCAL_INBOUND = {
  id: 2,
  port: 443,
  protocol: 'vless',
  tag: 'in-443-tcp',
  remark: 'Local',
  enable: true,
} as unknown as InboundOption;

const CLIENT = {
  email: 'Ivan',
  uuid: '11111111-1111-1111-1111-111111111111',
  subId: 'subid123',
  enable: true,
} as unknown as ClientRecord;

type SaveFn = ComponentProps<typeof ClientFormModal>['save'];

function saveMock() {
  return vi.fn<SaveFn>().mockResolvedValue({ success: true, msg: '', obj: null });
}

function renderEdit(save: SaveFn) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <ClientFormModal
          open
          mode="edit"
          client={CLIENT}
          inbounds={[NODE_INBOUND, LOCAL_INBOUND]}
          attachedIds={[1, 2]}
          attachedConnectionLimits={[
            {
              inboundId: 1,
              totalGB: 5 * 1024 ** 3,
              limitIp: 0,
              limitHwid: 0,
              expiryTime: 0,
              up: 1024 ** 3,
              down: 0,
            },
          ]}
          save={save}
          onOpenChange={() => {}}
        />
      </QueryClientProvider>
    </ThemeProvider>,
  );
}

function openLimitsTab() {
  const tab = Array.from(document.querySelectorAll('.ant-tabs-tab')).find(
    (t) => (t.textContent ?? '').trim() === 'Connection limits',
  );
  if (!tab) throw new Error('Connection limits tab not found');
  fireEvent.click(tab);
}

function limitInputs(): HTMLInputElement[] {
  return Array.from(
    document.querySelectorAll<HTMLInputElement>('.client-connection-limit .ant-input-number-input'),
  );
}

describe('ClientFormModal connection limits', () => {
  it('offers limits only for node inbounds and shows their usage', async () => {
    renderEdit(saveMock());
    openLimitsTab();
    await waitFor(() => expect(limitInputs()).toHaveLength(3));
    const rows = document.querySelectorAll('.client-connection-limit');
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain('BS');
    expect(rows[0].textContent).toContain('1.00 GB');
    expect(limitInputs()[0].value).toBe('5');
  });

  it('sends only changed connection limits in bytes', async () => {
    const save = saveMock();
    renderEdit(save);
    openLimitsTab();
    await waitFor(() => expect(limitInputs()).toHaveLength(3));
    fireEvent.change(limitInputs()[0], { target: { value: '10' } });
    fireEvent.change(limitInputs()[1], { target: { value: '2' } });
    fireEvent.change(limitInputs()[2], { target: { value: '3' } });
    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1].connectionLimits).toEqual([
      { inboundId: 1, totalGB: 10 * 1024 ** 3, limitIp: 2, limitHwid: 3, expiryTime: 0 },
    ]);
  });

  it('sends a delayed start as a negative duration', async () => {
    const save = saveMock();
    renderEdit(save);
    openLimitsTab();
    await waitFor(() => expect(limitInputs()).toHaveLength(3));
    const toggle = document.querySelector('.client-connection-limit .ant-switch');
    if (!toggle) throw new Error('delayed start switch not found');
    fireEvent.click(toggle);
    await waitFor(() => expect(limitInputs()).toHaveLength(4));
    fireEvent.change(limitInputs()[3], { target: { value: '10' } });
    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1].connectionLimits).toEqual([
      {
        inboundId: 1,
        totalGB: 5 * 1024 ** 3,
        limitIp: 0,
        limitHwid: 0,
        expiryTime: -10 * 86400000,
      },
    ]);
  });

  it('sends nothing when the limits are untouched', async () => {
    const save = saveMock();
    renderEdit(save);
    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1].connectionLimits).toEqual([]);
  });
});
