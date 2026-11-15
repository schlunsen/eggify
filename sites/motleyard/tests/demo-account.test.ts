import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  addDevice,
  emptyAccount,
  linkedAccount,
  parseAccount,
  removeDevice,
} from '../src/lib/demo-account.ts';

test('malformed or unlinked stored data produces an empty demo', () => {
  for (const raw of [
    null,
    '{broken',
    'null',
    '42',
    '{"linked":false,"devices":["desk"]}',
  ]) {
    assert.deepEqual(parseAccount(raw), emptyAccount());
  }
});

test('stored device IDs are validated, deduplicated, and limited', () => {
  assert.deepEqual(
    parseAccount(
      '{"linked":true,"devices":["desk","desk","unknown","laptop","extra","__proto__"]}',
    ),
    {
      linked: true,
      devices: ['desk', 'laptop', 'extra'],
    },
  );
});

test('activation requires a linked demo license and respects the three-slot limit', () => {
  assert.deepEqual(addDevice(emptyAccount()), emptyAccount());
  const original = linkedAccount();
  const full = addDevice(original);
  assert.equal(full.devices.length, 3);
  assert.equal(original.devices.length, 2);
  assert.deepEqual(addDevice(full), full);
});

test('a removed slot can be reused and state survives serialization', () => {
  const full = addDevice(linkedAccount());
  const released = removeDevice(full, 'desk');
  assert.equal(released.devices.length, 2);
  const restored = parseAccount(JSON.stringify(released));
  assert.deepEqual(addDevice(restored).devices, ['laptop', 'extra', 'desk']);
});
