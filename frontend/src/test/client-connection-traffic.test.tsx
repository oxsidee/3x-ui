import { describe, it, expect } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';

import ClientConnectionTraffic from '@/components/clients/ClientConnectionTraffic';
import type { ConnectionLimit, InboundOption } from '@/schemas/client';
import { renderWithProviders } from './test-utils';

const GB = 1024 ** 3;
const inboundsById = {
  1: { id: 1, tag: 'n1-in-8080-tcp', remark: 'BS' },
  5: { id: 5, tag: 'n2-in-10001-tcp', remark: 'NL' },
} as unknown as Record<number, InboundOption>;

function conn(over: Partial<ConnectionLimit>): ConnectionLimit {
  return {
    inboundId: 1,
    totalGB: 0,
    limitIp: 0,
    limitHwid: 0,
    expiryTime: 0,
    up: 0,
    down: 0,
    ...over,
  };
}

describe('ClientConnectionTraffic', () => {
  it('shows the connection with the least traffic left when collapsed', () => {
    renderWithProviders(
      <ClientConnectionTraffic
        inboundsById={inboundsById}
        connections={[
          conn({ inboundId: 5, totalGB: 50 * GB, up: 10 * GB }),
          conn({ inboundId: 1, totalGB: 10 * GB, up: 6 * GB }),
        ]}
        now={0}
      />,
    );
    expect(document.querySelector('.client-connection-traffic-name')?.textContent).toContain('BS');
    expect(document.querySelectorAll('.client-connection-traffic-item')).toHaveLength(0);
  });

  it('lists every limited connection when expanded', () => {
    renderWithProviders(
      <ClientConnectionTraffic
        inboundsById={inboundsById}
        connections={[
          conn({ inboundId: 1, totalGB: 10 * GB, limitHwid: 2 }),
          conn({ inboundId: 5, expiryTime: -10 * 86400000 }),
        ]}
        now={0}
      />,
    );
    fireEvent.click(screen.getByRole('button', { expanded: false }));
    const items = document.querySelectorAll('.client-connection-traffic-item');
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain('HWID: 2');
    expect(items[1].textContent).toContain('10 d after first use');
  });

  it('falls back to the plain traffic cell without connection limits', () => {
    renderWithProviders(<ClientConnectionTraffic inboundsById={inboundsById} up={1} down={2} />);
    expect(document.querySelector('.client-connection-traffic')).toBeNull();
    expect(document.querySelector('.client-traffic-cell')).not.toBeNull();
  });
});
