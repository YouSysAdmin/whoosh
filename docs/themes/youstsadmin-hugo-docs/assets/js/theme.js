// Theme behavior: the mobile nav drawer, the dark/light toggle, and the "On this
// page" TOC scrollspy. Shipped as a deferred bundle, so the DOM is parsed before
// this runs. The initial theme is resolved by a separate inline script in <head>
// (no flash); this only handles user interaction. Each block self-guards, so it is
// a no-op on pages that lack the relevant elements.

// Mobile navigation drawer.
(function () {
  var btn = document.querySelector('.cs-burger');
  var menu = document.getElementById('cs-mobile-menu');
  if (!btn || !menu) return;
  function setOpen(open) {
    btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    menu.classList.toggle('is-open', open);
    document.body.classList.toggle('cs-menu-open', open);
  }
  btn.addEventListener('click', function () {
    setOpen(btn.getAttribute('aria-expanded') !== 'true');
  });
  // Close when a link is followed or the backdrop (area outside the panel) is tapped.
  menu.addEventListener('click', function (e) {
    if (e.target === menu || e.target.closest('a')) setOpen(false);
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') setOpen(false);
  });
})();

// Theme toggle: flip data-theme on <html> and persist the choice.
(function () {
  var root = document.documentElement;
  var btn = document.querySelector('.cs-theme-toggle');
  if (!btn) return;
  function sync() {
    var light = root.getAttribute('data-theme') === 'light';
    btn.setAttribute('aria-pressed', light ? 'true' : 'false');
    btn.setAttribute('aria-label', light ? 'Switch to dark theme' : 'Switch to light theme');
  }
  sync();
  btn.addEventListener('click', function () {
    var next = root.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
    root.setAttribute('data-theme', next);
    try { localStorage.setItem('theme', next); } catch (e) {}
    sync();
  });
})();

// TOC scrollspy: highlight the "On this page" entry for the section currently in
// view and follow the page as it scrolls (like taskfile.dev). No-op when the page
// has no TOC or the browser lacks IntersectionObserver.
(function () {
  var toc = document.querySelector('.cs-toc');
  if (!toc || !('IntersectionObserver' in window)) return;
  var links = Array.prototype.slice.call(toc.querySelectorAll('a[href^="#"]'));
  if (!links.length) return;

  var linkFor = {}; // heading id -> TOC <a>
  var headings = []; // heading elements, in document order
  links.forEach(function (a) {
    var id = decodeURIComponent((a.getAttribute('href') || '').slice(1));
    var el = id && document.getElementById(id);
    if (el) {
      linkFor[id] = a;
      headings.push(el);
    }
  });
  if (!headings.length) return;

  var current = null;
  function activate(a) {
    if (a === current) return;
    if (current) {
      current.classList.remove('is-active');
      current.removeAttribute('aria-current');
    }
    current = a || null;
    if (a) {
      a.classList.add('is-active');
      a.setAttribute('aria-current', 'true');
    }
  }

  var visible = new Set();
  function pick() {
    // The first heading (document order) inside the active band wins. If the band
    // is empty - scrolled to rest between two headings - keep the last heading
    // that sits above it, so something always stays highlighted.
    for (var i = 0; i < headings.length; i++) {
      if (visible.has(headings[i])) {
        activate(linkFor[headings[i].id]);
        return;
      }
    }
    var above = null;
    for (var j = 0; j < headings.length; j++) {
      if (headings[j].getBoundingClientRect().top < 100) above = headings[j];
      else break;
    }
    if (above) activate(linkFor[above.id]);
  }

  // Active band: from just below the sticky nav (~80px) down to 30% of the
  // viewport, so a heading lights up as it reaches the top of the reading area.
  var io = new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (e.isIntersecting) visible.add(e.target);
      else visible.delete(e.target);
    });
    pick();
  }, { rootMargin: '-80px 0px -70% 0px', threshold: 0 });
  headings.forEach(function (h) { io.observe(h); });

  // Reflect a clicked entry immediately, before the scroll settles.
  links.forEach(function (a) {
    a.addEventListener('click', function () { activate(a); });
  });
})();
