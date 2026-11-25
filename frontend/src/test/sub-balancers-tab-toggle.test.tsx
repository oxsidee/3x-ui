import { describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';

import SubscriptionBalancersTab from '@/pages/settings/SubscriptionBalancersTab';
import type { AllSetting } from '@/models/setting';
import { renderWithProviders } from './test-utils';

const update = vi.fn(async () => ({ success: true }));

vi.mock('@/api/queries/useSubBalancersQuery', () => ({
  useSubBalancersQuery: () => ({
    balancers: [
      {
        id: 4,
        remark: 'auto',
        strategy: 'leastPing',
        inboundIds: [1],
        sortOrder: 1,
        enabled: true,
        hideMembers: true,
      },
    ],
    loading: false,
    fetched: true,
    fetchError: '',
    refetch: vi.fn(),
  }),
}));

vi.mock('@/api/queries/useSubBalancerMutations', () => ({
  useSubBalancerMutations: () => ({ create: vi.fn(), update, remove: vi.fn() }),
}));

vi.mock('@/api/queries/useInboundOptions', () => ({
  useInboundOptions: () => ({ data: [], isLoading: false }),
}));

describe('SubscriptionBalancersTab', () => {
  it('keeps hideMembers when the enabled switch is toggled', async () => {
    renderWithProviders(
      <SubscriptionBalancersTab
        allSetting={{ subJsonObservatory: '' } as AllSetting}
        updateSetting={() => {}}
      />,
    );
    const toggle = document.querySelector('.ant-table .ant-switch');
    if (!toggle) throw new Error('Enabled switch not found');
    fireEvent.click(toggle);
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
    expect(update).toHaveBeenCalledWith(
      4,
      expect.objectContaining({ enabled: false, hideMembers: true }),
    );
  });
});
