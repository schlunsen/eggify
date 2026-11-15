import { element } from '../lib/dom.ts';
import {
  parseSetup,
  machineName,
  setupLink,
  setupBrief,
  type RentalSetup,
} from '../lib/rental.ts';

export function initConfigurator(): void {
  const form = element<HTMLFormElement>('#rental-config');
  const review = element<HTMLElement>('#review-panel');
  const status = element<HTMLElement>('#config-status');
  const initial = parseSetup(new URLSearchParams(window.location.search));

  form.querySelectorAll<HTMLInputElement>('input').forEach((input) => {
    if (input.name === 'machine')
      input.checked = input.value === initial.machine;
    if (input.name === 'tools')
      input.checked = initial.tools.some((tool) => tool === input.value);
    if (input.name === 'work') input.checked = input.value === initial.work;
  });

  function currentSetup(): RentalSetup {
    const fields = new FormData(form);
    return parseSetup(
      new URLSearchParams({
        machine: String(fields.get('machine')),
        tools: fields.getAll('tools').join(','),
        work: String(fields.get('work')),
      }),
    );
  }

  function render(): void {
    const setup = currentSetup();
    element('#summary-title').textContent = machineName(setup.machine);
    element('#summary-tools').textContent =
      setup.tools.join(' + ') || 'Bring your own tools';
    element('#summary-work').textContent = setup.work;
    element('#review-description').textContent = [
      `${machineName(setup.machine)} with Motleyard.`,
      setup.tools.length
        ? `Prepared tools: ${setup.tools.join(' and ')}.`
        : 'No preselected agent tools.',
      `Your focus: ${setup.work}.`,
    ].join(' ');
    status.textContent = 'A starting point. No commitment.';
  }

  form.addEventListener('submit', (event) => event.preventDefault());
  form.addEventListener('change', () => {
    render();
    review.hidden = true;
  });
  element('#review').addEventListener('click', () => {
    review.hidden = false;
    review.focus();
    review.scrollIntoView({
      block: 'nearest',
      behavior: matchMedia('(prefers-reduced-motion: reduce)').matches
        ? 'instant'
        : 'smooth',
    });
  });
  element('#download').addEventListener('click', () => {
    const setup = currentSetup();
    const url = URL.createObjectURL(
      new Blob([setupBrief(setup)], { type: 'text/plain' }),
    );
    const link = document.createElement('a');
    link.href = url;
    link.download = `motleyard-${setup.machine}-setup.txt`;
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    status.textContent = 'Your setup brief has been downloaded.';
  });
  element('#share').addEventListener('click', async () => {
    const url = setupLink(currentSetup(), window.location.origin);
    try {
      await navigator.clipboard.writeText(url.href);
      const isLocal = ['localhost', '127.0.0.1'].includes(url.hostname);
      status.textContent = isLocal
        ? 'Setup link copied. This local link works on this computer.'
        : 'Setup link copied.';
    } catch {
      status.textContent = `Copy this setup link: ${url.href}`;
    }
  });
  render();
}
