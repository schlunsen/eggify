export function initMotion(): void {
  const motion = window.matchMedia('(prefers-reduced-motion: reduce)');

  // Native cross-document transitions preserve ordinary links and browser history.
  window.addEventListener('pagereveal', (event) => {
    const transition = (
      event as Event & {
        viewTransition?: { finished: Promise<void> };
      }
    ).viewTransition;
    if (!transition) return;
    document.documentElement.classList.add('yard-arriving');
    void transition.finished
      .finally(() => document.documentElement.classList.remove('yard-arriving'))
      .catch(() => {});
  });

  // Never hide content while waiting for JavaScript or IntersectionObserver.
  if (!('IntersectionObserver' in window) || motion.matches) return;
  const observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        if (!motion.matches) entry.target.classList.add('yard-reveal');
        observer.unobserve(entry.target);
      }
    },
    { threshold: 0.08 },
  );

  document
    .querySelectorAll<HTMLElement>(
      '.section-intro, .feature, .step, .offer, .machine-layout, .license-box',
    )
    .forEach((target) => {
      if (target.getBoundingClientRect().top > window.innerHeight)
        observer.observe(target);
    });

  motion.addEventListener('change', (event) => {
    if (!event.matches) return;
    observer.disconnect();
    document.querySelectorAll('.yard-reveal').forEach((target) => {
      target.classList.remove('yard-reveal');
    });
  });
}
