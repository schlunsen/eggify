import { element } from '../lib/dom.ts';

/** Demonstrates the sign-in screens; never sends email or authenticates a user. */
export function initSignInDemo(): void {
  const signIn = element<HTMLElement>('#signin-card');
  const emailPreview = element<HTMLElement>('#email-card');
  element<HTMLFormElement>('#signin-form').addEventListener(
    'submit',
    (event) => {
      event.preventDefault();
      signIn.hidden = true;
      emailPreview.hidden = false;
      emailPreview.focus();
    },
  );
  element('#back-signin').addEventListener('click', () => {
    emailPreview.hidden = true;
    signIn.hidden = false;
    element<HTMLInputElement>('#email').focus();
  });
}
