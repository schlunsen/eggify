import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseSetup, setupBrief, setupLink } from '../src/lib/rental.ts';

test('configuration links round-trip, including an empty tool selection', () => {
  for (const tools of ['Codex', '']) {
    const setup = parseSetup(
      new URLSearchParams({ machine: 'max', tools, work: 'Creative tools' }),
    );
    const url = setupLink(setup, 'https://example.test');
    assert.deepEqual(parseSetup(url.searchParams), setup);
  }
});

test('unknown query values cannot inject arbitrary machine or tool labels', () => {
  const setup = parseSetup(
    new URLSearchParams('machine=invalid&tools=Codex,evil,Codex&work=invalid'),
  );
  assert.deepEqual(setup, {
    machine: 'pro',
    tools: ['Codex'],
    work: 'Web & apps',
  });
});

test('download includes the selected setup and clearly states that no order exists', () => {
  const brief = setupBrief(
    parseSetup(new URLSearchParams('machine=max&tools=')),
  );
  assert.ok(brief.includes('Mac M5 Max\nWorkspace: Motleyard'));
  assert.ok(brief.includes('Agent tools: Bring your own'));
  assert.ok(brief.includes('No order has been placed.'));
});
