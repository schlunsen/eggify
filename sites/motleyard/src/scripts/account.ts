import { element } from '../lib/dom.ts';
import {
  DEMO_SERIAL,
  DEMO_STORAGE_KEY,
  SEAT_LIMIT,
  deviceLabels,
  emptyAccount,
  linkedAccount,
  parseAccount,
  addDevice,
  removeDevice,
  type DemoAccount,
  type DeviceId,
} from '../lib/demo-account.ts';

/** UI-only prototype: no credentials, network calls, or real activations. */
export function initAccountDemo(): void {
  let state = emptyAccount();
  try {
    state = parseAccount(sessionStorage.getItem(DEMO_STORAGE_KEY));
  } catch {
    /* Storage may be blocked. */
  }

  const serial = element<HTMLInputElement>('#serial');
  const licenseStatus = element<HTMLElement>('#license-status');
  const deviceStatus = element<HTMLElement>('#device-status');
  const addButton = element<HTMLButtonElement>('#add-demo-device');
  const dialog = element<HTMLDialogElement>('#remove-dialog');
  let pendingDevice: DeviceId | null = null;
  let dialogTrigger: HTMLButtonElement | null = null;

  function update(next: DemoAccount): void {
    state = next;
    try {
      sessionStorage.setItem(DEMO_STORAGE_KEY, JSON.stringify(state));
    } catch {
      /* In-memory demo remains usable. */
    }
    render();
  }

  function deviceRow(id: DeviceId): HTMLElement {
    const row = document.createElement('div');
    row.className = 'device-row';
    const icon = document.createElement('span');
    icon.className = 'device-icon';
    icon.textContent = '▣';
    icon.setAttribute('aria-hidden', 'true');
    const description = document.createElement('div');
    const title = document.createElement('strong');
    title.textContent = deviceLabels[id];
    const subtitle = document.createElement('small');
    subtitle.textContent = 'DEMO ACTIVATION';
    description.append(title, subtitle);
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = 'Deactivate';
    button.setAttribute('aria-label', `Deactivate ${deviceLabels[id]}`);
    button.addEventListener('click', () => {
      pendingDevice = id;
      dialogTrigger = button;
      element('#remove-description').textContent =
        `${deviceLabels[id]} will be removed from this demo license.`;
      dialog.showModal();
    });
    row.append(icon, description, button);
    return row;
  }

  function render(): void {
    element<HTMLElement>('#unlinked-license').hidden = state.linked;
    element<HTMLElement>('#linked-license').hidden = !state.linked;
    element('#license-title').textContent = state.linked
      ? 'Your workspace is connected.'
      : 'Give your serial a home.';
    element('#seat-count').textContent = String(state.devices.length);
    element('#seat-description').textContent = state.linked
      ? 'Sample activations. Free a slot to make room for another computer.'
      : 'Link the demo license to explore computer management.';
    addButton.hidden = !state.linked || state.devices.length >= SEAT_LIMIT;
    element('#device-list').replaceChildren(...state.devices.map(deviceRow));
  }

  element<HTMLFormElement>('#link-license').addEventListener(
    'submit',
    (event) => {
      event.preventDefault();
      if (serial.value.trim().toUpperCase() !== DEMO_SERIAL) {
        licenseStatus.textContent = `This demo accepts ${DEMO_SERIAL} only. No real license was checked.`;
        serial.setAttribute('aria-invalid', 'true');
        serial.focus();
        return;
      }
      serial.removeAttribute('aria-invalid');
      update(linkedAccount());
      licenseStatus.textContent =
        'Demo license linked. Two sample computer slots are in use.';
      // The submitted form is now hidden, so move focus to a visible control.
      addButton.focus();
    },
  );

  addButton.addEventListener('click', () => {
    const next = addDevice(state);
    const added = next.devices.find((id) => !state.devices.includes(id));
    if (!added) return;
    update(next);
    deviceStatus.textContent = `${deviceLabels[added]} activated in this demo.`;
    if (addButton.hidden)
      element<HTMLButtonElement>('#device-list button').focus();
  });
  element('#cancel-remove').addEventListener('click', () => dialog.close());
  element('#confirm-remove').addEventListener('click', () => {
    if (!pendingDevice) return;
    const removed = pendingDevice;
    pendingDevice = null;
    dialog.close();
    update(removeDevice(state, removed));
    deviceStatus.textContent = `${deviceLabels[removed]} removed. A demo slot is now available.`;
    addButton.focus();
  });
  dialog.addEventListener('close', () => {
    pendingDevice = null;
    if (dialogTrigger?.isConnected) dialogTrigger.focus();
  });
  element('#reset-account').addEventListener('click', () => {
    update(emptyAccount());
    licenseStatus.textContent = 'Demo reset.';
    deviceStatus.textContent = '';
    serial.value = DEMO_SERIAL;
    serial.removeAttribute('aria-invalid');
  });
  render();
}
