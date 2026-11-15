function (o) {
  // Injected by the desktop app (machine_switch.go) into whatever page the
  // window shows, this Mac's or another machine's, so it depends on nothing
  // in the dashboard. Styles go through the CSSOM and motion through the
  // Web Animations API, which the pages' CSP allows.
  //
  // Fade through a neutral loading surface across document navigations.
  // Only opacity animates. The destination remains covered through its
  // bootstrap page and mounting, with a way back if connecting is slow.
  var ID = '__eggify_machine_switch';
  var reduce = window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches;
  var ease = 'cubic-bezier(.4,0,.2,1)';
  var OUT_MS = 140, IN_MS = 200, MOUNT_CAP_MS = 10000, LOADING_AFTER = 400, SLOW_AFTER = 5000;
  var seq = o.seq | 0;
  var start = typeof o.start === 'number' && o.start > 0 ? o.start : Date.now();

  function post(ev, rest) {
    try { window.webkit.messageHandlers.external.postMessage('eggify-machine:' + ev + ' ' + rest); } catch (e) { /* not in the app */ }
  }
  function build(root) {
    var el = document.getElementById(ID);
    if (el) return el;
    el = document.createElement('div');
    el.id = ID;
    el.setAttribute('role', 'group');
    el.setAttribute('aria-label', 'Switching instance');
    var s = el.style;
    s.position = 'fixed'; s.inset = '0'; s.zIndex = '2147483647';
    s.display = 'flex'; s.alignItems = 'center'; s.justifyContent = 'center';
    s.opacity = '0'; s.pointerEvents = 'auto'; s.overflow = 'hidden'; s.contain = 'strict';
    s.font = '600 15px -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif';
    var card = document.createElement('div');
    card.dataset.part = 'card';
    card.style.cssText = 'display:flex;flex-direction:column;align-items:center;gap:10px;';
    var cap = document.createElement('div');
    cap.dataset.part = 'cap';
    cap.style.cssText = 'font-size:11px;letter-spacing:.18em;text-transform:uppercase;opacity:.62;';
    var row = document.createElement('div');
    row.style.cssText = 'display:flex;align-items:center;gap:12px;';
    var dot = document.createElement('div');
    dot.dataset.part = 'dot';
    dot.style.cssText = 'width:14px;height:17px;border-radius:50% 50% 48% 48% / 60% 60% 40% 40%;';
    var name = document.createElement('div');
    name.dataset.part = 'name';
    name.style.cssText = 'font-size:20px;font-weight:600;letter-spacing:-.01em;';
    var loading = document.createElement('div');
    loading.dataset.part = 'loading';
    loading.textContent = 'Loading…';
    loading.style.cssText = 'font-size:12px;font-weight:400;opacity:0;';
    var slow = document.createElement('div');
    slow.dataset.part = 'slow';
    slow.style.cssText = 'display:none;flex-direction:column;align-items:center;gap:10px;margin-top:10px;font-size:13px;font-weight:500;';
    var slowText = document.createElement('div');
    slowText.dataset.part = 'slow-text';
    slowText.style.cssText = 'opacity:.8;';
    var stay = document.createElement('button');
    stay.type = 'button';
    stay.dataset.part = 'stay';
    stay.style.cssText = 'font:inherit;font-weight:600;padding:6px 14px;border-radius:8px;border:1px solid rgba(255,255,255,.35);background:rgba(255,255,255,.12);color:inherit;cursor:pointer;';
    slow.appendChild(slowText); slow.appendChild(stay);
    row.appendChild(dot); row.appendChild(name);
    card.appendChild(cap); card.appendChild(row); card.appendChild(loading); card.appendChild(slow);
    el.appendChild(card);
    root.appendChild(el);
    return el;
  }

  function part(el, p) { return el.querySelector('[data-part="' + p + '"]'); }

  function paint(el) {
    var h = o.hue | 0;
    el.style.background = '#18181b';
    el.style.color = '#fafafa';
    part(el, 'cap').textContent = o.here ? 'Local instance' : 'Remote instance';
    part(el, 'name').textContent = o.name;
    part(el, 'dot').style.background = 'hsl(' + h + ' 75% 65%)';
    part(el, 'loading').style.opacity = '0';
    part(el, 'slow').style.display = 'none';
    part(el, 'slow-text').textContent = 'Still connecting to ' + o.name + '…';
    var stay = part(el, 'stay');
    stay.textContent = 'Stay on ' + (o.from || 'This Mac');
    stay.onclick = function () { post('cancel', seq + ' 0'); };
  }

  // Timers belong to the overlay; a newer switch on the same page clears them.
  function clearTimers(el) {
    (el.__timers || []).forEach(function (t) { clearTimeout(t); });
    el.__timers = [];
  }
  function later(el, ms, fn) { el.__timers.push(setTimeout(fn, Math.max(0, ms))); }

  // Show loading feedback only for slower switches, then offer a way back.
  function waiting(el) {
    var since = Date.now() - start;
    later(el, LOADING_AFTER - since, function () {
      part(el, 'loading').animate([{ opacity: 0 }, { opacity: 1 }], { duration: reduce ? 0 : 150, fill: 'forwards' });
    });
    later(el, SLOW_AFTER - since, function () {
      var slow = part(el, 'slow');
      slow.style.display = 'flex';
      slow.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 220, fill: 'backwards' });
    });
  }

  // Invalidate pending frames, animation callbacks and fallback cleanup
  // when a rapid shortcut reuses this same overlay.
  function reset(el) {
    clearTimers(el);
    el.__run = (el.__run || 0) + 1;
    el.getAnimations({ subtree: true }).forEach(function (a) { a.onfinish = null; a.cancel(); });
    el.style.pointerEvents = 'auto';
    return el.__run;
  }

  function out() {
    var el = build(document.documentElement);
    var opacity = getComputedStyle(el).opacity;
    reset(el);
    paint(el);
    el.style.opacity = '1';
    el.animate([{ opacity: opacity }, { opacity: 1 }], { duration: reduce ? 80 : OUT_MS, easing: ease, fill: 'backwards' });
    // Remember where the owner was on this machine (the app opens it there next time).
    post('left', seq + ' ' + encodeURIComponent(location.pathname + location.search).slice(0, 600));
    waiting(el);
  }

  function into() {
    var root = document.documentElement;
    if (!root) { setTimeout(into, 0); return; }
    var t0 = Date.now();
    var el = build(root);
    var run = reset(el);
    paint(el);
    // Cover the destination before its first paint, including bootstrap hops.
    el.style.opacity = '1';
    waiting(el);
    // The machine proxy's one-line bootstrap page hands over to the real
    // page right away; stay covered there.
    if (location.pathname === '/__desktop/machine-bootstrap') return;
    // A newer switch is about to navigate away from this page.
    if (o.hold) return;

    var done = false, readyAt = 0;
    function ready() {
      if (document.readyState === 'loading') return false;
      var app = document.getElementById('__nuxt');
      if (app) return app.children.length > 0;
      return document.readyState !== 'loading' && !!document.body && document.body.children.length > 0;
    }
    function frames(n, fn) {
      // Two painted frames; a hidden page gets no frames, so a timer backs it up.
      var fired = false;
      var go = function () { if (!fired && el.__run === run) { fired = true; fn(); } };
      later(el, 120, go);
      (function step(k) { requestAnimationFrame(function () { if (k <= 1) go(); else step(k - 1); }); })(n);
    }
    function reveal() {
      if (done || el.__run !== run) return;
      done = true;
      clearTimers(el);
      post('reveal', seq + ' ' + (Date.now() - t0));
      var bye = function () {
        if (el.__run !== run) return;
        clearTimers(el);
        if (el.isConnected) el.remove();
      };
      var duration = reduce ? 100 : IN_MS;
      var fade = el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: duration, easing: ease, fill: 'forwards' });
      fade.onfinish = bye;
      // Hidden windows or an interrupted animation must not strand the cover.
      later(el, duration + 600, bye);
    }

    (function wait() {
      if (done || el.__run !== run) return;
      var now = Date.now();
      var ok = ready();
      if (ok && !readyAt) { readyAt = now; post('ready', seq + ' ' + (now - t0)); }
      // Give the page up to MOUNT_CAP_MS to mount, then show whatever is there.
      if (ok || now - t0 > MOUNT_CAP_MS) {
        if (!readyAt) post('ready', seq + ' -1');
        frames(2, reveal);
        return;
      }
      // Timers, not frames: they run in hidden windows too, and the check is cheap.
      later(el, 16, wait);
    })();
  }

  if (o.phase === 'out') out(); else if (o.phase === 'in') into();
}
