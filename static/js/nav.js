// Shared nav component — two-level navigation
// Level 1: Logo, search, user/auth controls
// Level 2: Page links
(function() {
    const navLinks = [
        { href: '/', label: 'Home' },
        { href: '/rankings', label: 'Rankings' },
        { href: '/compare', label: 'Compare' },
        { href: '/network', label: 'Network' },
        { href: '/timeline', label: 'Timeline' },
        { href: '/stats', label: 'Stats' },
        { href: '/predictor', label: 'Predictor' },
    ];

    // Fixed poster-style backdrop behind everything (styled in shared.css)
    const bg = document.createElement('div');
    bg.className = 'page-bg';
    bg.setAttribute('aria-hidden', 'true');
    document.body.insertBefore(bg, document.body.firstChild);

    // Photo wallpaper — shared pool with joshi.fyi (see /api/background).
    // Blur-up: the tiny placeholder shows heavily blurred right away and the
    // real photo fades in once decoded; until the fetch lands the gradient
    // starfield backdrop stands in.
    fetch('/api/background').then(r => r.json()).then(b => {
        if (!b || !b.large) return;
        bg.style.setProperty('--photo', `url('${b.large}')`);
        bg.style.setProperty('--photo-m', `url('${b.mobile || b.large}')`);
        if (b.placeholder) bg.style.setProperty('--photo-p', `url('${b.placeholder}')`);
        bg.classList.add('has-photo');
        const src = matchMedia('(max-width: 700px)').matches ? (b.mobile || b.large) : b.large;
        const photo = new Image();
        photo.src = src;
        if (!photo.complete) {
            bg.classList.add('bg-wait');
            const reveal = () => bg.classList.remove('bg-wait');
            if (photo.decode) photo.decode().then(reveal, reveal);
            else photo.onload = photo.onerror = reveal;
        }
    }).catch(() => {});

    const header = document.createElement('header');
    header.id = 'site-header';
    header.innerHTML = `
        <div class="nav-top">
            <div class="nav-brand">
                <a href="#" id="brand-icon" title="Random wrestler!"><img src="/static/logo.webp" alt="Joshitori" class="brand-logo"></a>
                <a href="/"><span class="brand-text"><span><span class="brand-joshi">Joshi</span><span class="brand-tori">tori</span></span><span class="brand-sub">(hoshitori for joshi)</span></span></a>
            </div>
            <nav class="nav-pages">
                <ul class="nav-links">
                    ${navLinks.map(l => `<li><a href="${l.href}">${l.label}</a></li>`).join('\n                    ')}
                </ul>
            </nav>
            <div class="nav-top-right">
                <div id="nav-search-wrap">
                    <input type="text" placeholder="Search wrestlers..." autocomplete="off">
                </div>
                <button class="nav-burger" aria-label="Menu">☰</button>
            </div>
        </div>
    `;

    document.body.insertBefore(header, document.body.firstChild);

    // navhide: X-mobile header behavior — the sticky bar slides away as you
    // scroll down and returns the moment you scroll up (or near the top).
    let lastY = window.scrollY;
    window.addEventListener('scroll', function() {
        const y = window.scrollY;
        if (y < 80 || y < lastY) header.classList.remove('nav-hidden');
        else if (y > lastY + 2) header.classList.add('nav-hidden');
        lastY = y;
    }, { passive: true });

    // Preload wrestler list on page load so random click is instant
    let cachedWrestlers = null;
    fetch('/api/wrestler-names').then(r => r.json()).then(data => { cachedWrestlers = data; }).catch(() => {});

    // Logo icon — instant random wrestler, no delay needed
    const brandIcon = document.getElementById('brand-icon');
    brandIcon.addEventListener('click', function(e) {
        e.preventDefault();
        e.stopPropagation();
        try {
            const wrestlers = cachedWrestlers;
            if (wrestlers && wrestlers.length > 0) {
                const random = wrestlers[Math.floor(Math.random() * wrestlers.length)];
                window.location.href = '/wrestler/' + random.id;
            } else {
                window.location.href = '/';
            }
        } catch(err) {
            window.location.href = '/';
        }
    });
})();
