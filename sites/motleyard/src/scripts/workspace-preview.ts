import { projects } from '../data/projects.ts';
import { element } from '../lib/dom.ts';

export function initWorkspacePreview(): void {
  const tabs = [
    ...document.querySelectorAll<HTMLButtonElement>('[data-project]'),
  ];

  function setLines(selector: string, lines: string[]): void {
    const target = element(selector);
    target.replaceChildren();
    lines.forEach((line, index) => {
      if (index) target.append(document.createElement('br'));
      target.append(document.createTextNode(line));
    });
  }

  function select(tab: HTMLButtonElement): void {
    const project = projects[tab.dataset.project ?? ''];
    if (!project) return;
    tabs.forEach((button) => {
      const active = button === tab;
      button.classList.toggle('selected', active);
      button.setAttribute('aria-selected', String(active));
      button.tabIndex = active ? 0 : -1;
    });
    project.tasks.forEach((lines, index) =>
      setLines(`#task${index + 1}`, lines),
    );
    project.details.forEach((lines, index) =>
      setLines(`#detail${index + 1}`, lines),
    );
    project.progress.forEach((value, index) => {
      element<HTMLElement>(`#prog${index + 1}`).style.width = `${value}%`;
    });
  }

  tabs.forEach((tab, index) => {
    tab.tabIndex = index === 0 ? 0 : -1;
    tab.addEventListener('click', () => select(tab));
    tab.addEventListener('keydown', (event) => {
      const destinations: Record<string, number> = {
        Home: 0,
        End: tabs.length - 1,
        ArrowRight: (index + 1) % tabs.length,
        ArrowLeft: (index - 1 + tabs.length) % tabs.length,
      };
      const destination = destinations[event.key];
      if (destination === undefined) return;
      event.preventDefault();
      const next = tabs[destination];
      if (next) {
        select(next);
        next.focus();
      }
    });
  });
}
