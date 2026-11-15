/** Fail clearly when a controller and its page markup get out of sync. */
export function element<T extends Element = HTMLElement>(
  selector: string,
  root: ParentNode = document,
): T {
  const match = root.querySelector<T>(selector);
  if (!match) throw new Error(`Missing Motleyard element: ${selector}`);
  return match;
}
