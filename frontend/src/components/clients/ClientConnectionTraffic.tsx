import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Typography } from 'antd';
import { DownOutlined, UpOutlined } from '@ant-design/icons';

import ClientTrafficCell from '@/components/clients/ClientTrafficCell';
import { formatInboundLabel } from '@/lib/inbounds/label';
import type { ConnectionLimit, InboundOption } from '@/schemas/client';
import './ClientConnectionTraffic.css';

export interface ClientConnectionTrafficProps {
  connections?: ConnectionLimit[] | null;
  inboundsById: Record<number, InboundOption>;
  up?: number;
  down?: number;
  total?: number;
  enabled?: boolean;
  trafficDiff?: number;
  compact?: boolean;
  now?: number;
}

const DAY_MS = 86400000;

export function leastRemainingConnection(
  connections: ConnectionLimit[],
): ConnectionLimit | undefined {
  let best: ConnectionLimit | undefined;
  let bestLeft = Infinity;
  for (const c of connections) {
    if (c.totalGB <= 0) continue;
    const left = c.totalGB - (c.up + c.down);
    if (left < bestLeft) {
      bestLeft = left;
      best = c;
    }
  }
  return best;
}

export default function ClientConnectionTraffic({
  connections,
  inboundsById,
  up,
  down,
  total,
  enabled = true,
  trafficDiff = 0,
  compact = false,
  now,
}: ClientConnectionTrafficProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [renderedAt] = useState(() => Date.now());
  const current = now ?? renderedAt;
  const rows = useMemo(() => connections ?? [], [connections]);
  const head = useMemo(() => leastRemainingConnection(rows), [rows]);

  if (rows.length === 0) {
    return (
      <ClientTrafficCell
        compact={compact}
        up={up}
        down={down}
        total={total}
        enabled={enabled}
        trafficDiff={trafficDiff}
      />
    );
  }

  const label = (c: ConnectionLimit) => {
    const ib = inboundsById[c.inboundId];
    return ib ? formatInboundLabel(ib.tag, ib.remark) : `#${c.inboundId}`;
  };

  const expiryText = (c: ConnectionLimit) => {
    if (c.expiryTime < 0) {
      return t('pages.clients.connectionExpiresAfterFirstUse', {
        days: Math.round(-c.expiryTime / DAY_MS),
      });
    }
    if (c.expiryTime > 0 && c.expiryTime <= current) return t('pages.clients.connectionExpired');
    if (c.expiryTime > 0) {
      return t('pages.clients.connectionExpiresOn', {
        date: new Date(c.expiryTime).toLocaleDateString(),
      });
    }
    return '';
  };

  const cell = (c: ConnectionLimit) => (
    <ClientTrafficCell
      compact={compact}
      up={c.up}
      down={c.down}
      total={c.totalGB}
      enabled={enabled}
      trafficDiff={trafficDiff}
    />
  );

  return (
    <div className="client-connection-traffic">
      <div className="client-connection-traffic-head">
        <div className="client-connection-traffic-main">
          <Typography.Text type="secondary" className="client-connection-traffic-name">
            {head ? label(head) : t('pages.clients.tabConnectionLimits')}
          </Typography.Text>
          {head ? cell(head) : <ClientTrafficCell compact={compact} up={up} down={down} />}
        </div>
        <Button
          type="text"
          size="small"
          aria-expanded={open}
          aria-label={t('pages.clients.tabConnectionLimits')}
          icon={open ? <UpOutlined aria-hidden /> : <DownOutlined aria-hidden />}
          onClick={() => setOpen((v) => !v)}
        />
      </div>
      {open && (
        <ul className="client-connection-traffic-list">
          {rows.map((c) => {
            const extras = [
              expiryText(c),
              c.limitIp > 0 ? `IP: ${c.limitIp}` : '',
              c.limitHwid > 0 ? `HWID: ${c.limitHwid}` : '',
            ].filter(Boolean);
            return (
              <li key={c.inboundId} className="client-connection-traffic-item">
                <Typography.Text strong>{label(c)}</Typography.Text>
                {c.totalGB > 0 && cell(c)}
                {extras.length > 0 && (
                  <Typography.Text type="secondary">{extras.join(' · ')}</Typography.Text>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
