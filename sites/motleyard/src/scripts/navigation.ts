import { element } from '../lib/dom.ts';

export function initNavigation(): void {
  const button = element<HTMLButtonElement>('.menu-toggle');
  const navigation = element<HTMLElement>('#main-nav');

  function setOpen(open: boolean): void {
    button.setAttribute('aria-expanded', String(open));
    button.textContent = open ? 'Close −' : 'Menu +';
    navigation.classList.toggle('open', open);
  }

  button.addEventListener('click', () => {
    setOpen(button.getAttribute('aria-expanded') !== 'true');
  });
  document.addEventListener('keydown', (event) => {
    if (
      event.key !== 'Escape' ||
      button.getAttribute('aria-expanded') !== 'true'
    )
      return;
    setOpen(false);
    button.focus();
  });
  navigation.addEventListener('click', (event) => {
    if (event.target instanceof Element && event.target.closest('a'))
      setOpen(false);
  });
  window.addEventListener('pageshow', () => setOpen(false));
}
