/** Sample data only. This module must never be used for access control. */
export const DEMO_SERIAL = 'MOTLEY-DEMO';
export const DEMO_STORAGE_KEY = 'motleyard-demo-account-v1';
export const SEAT_LIMIT = 3;
export const deviceLabels = {
  desk: 'Studio Mac',
  laptop: 'Travel Mac',
  extra: 'Project Mac',
} as const;
export type DeviceId = keyof typeof deviceLabels;
export interface DemoAccount {
  linked: boolean;
  devices: DeviceId[];
}
export const emptyAccount = (): DemoAccount => ({ linked: false, devices: [] });
export const linkedAccount = (): DemoAccount => ({
  linked: true,
  devices: ['desk', 'laptop'],
});

function isDeviceId(value: unknown): value is DeviceId {
  return typeof value === 'string' && Object.hasOwn(deviceLabels, value);
}

export function parseAccount(raw: string | null): DemoAccount {
  try {
    const value: unknown = JSON.parse(raw ?? 'null');
    if (typeof value !== 'object' || value === null) return emptyAccount();
    const data = value as Record<string, unknown>;
    if (data.linked !== true || !Array.isArray(data.devices))
      return emptyAccount();
    return {
      linked: true,
      devices: [...new Set(data.devices.filter(isDeviceId))].slice(
        0,
        SEAT_LIMIT,
      ),
    };
  } catch {
    return emptyAccount();
  }
}

export function addDevice(account: DemoAccount): DemoAccount {
  if (!account.linked || account.devices.length >= SEAT_LIMIT) return account;
  const next = (Object.keys(deviceLabels) as DeviceId[]).find(
    (id) => !account.devices.includes(id),
  );
  return next ? { ...account, devices: [...account.devices, next] } : account;
}

export function removeDevice(account: DemoAccount, id: DeviceId): DemoAccount {
  return {
    ...account,
    devices: account.devices.filter((device) => device !== id),
  };
}
