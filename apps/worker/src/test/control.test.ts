import assert from 'node:assert/strict';
import { test } from 'node:test';

import { isControlMessage } from '../transport/control.js';

test('isControlMessage guards malformed payloads', () => {
  assert.equal(
    isControlMessage({ type: 'auth-login', accountId: 'a', platform: 'instagram' }),
    true,
  );
  assert.equal(isControlMessage({ type: 'auth-login' }), false);
  assert.equal(isControlMessage(null), false);
  assert.equal(isControlMessage('nope'), false);
});

test('control payload with 2FA code still validates', () => {
  const msg = {
    type: 'auth-input',
    accountId: 'a1',
    platform: 'instagram',
    payload: { value: '123456' },
  };
  assert.equal(isControlMessage(msg), true);
});
