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

    const header = document.createElement('header');
    header.id = 'site-header';
    header.innerHTML = `
        <div class="nav-top">
            <div class="nav-brand">
                <a href="#" id="brand-icon" title="Random wrestler!"><img src="/static/logo.webp" alt="Joshitori" class="brand-logo"></a>
                <a href="/"><span class="brand-text"><span><span class="brand-joshi">Joshi</span><span class="brand-tori">tori</span></span><span class="brand-sub">(hoshitori for joshi)</span></span></a>
            </div>
            <div id="nav-search-wrap">
                <input type="text" placeholder="Search wrestlers..." autocomplete="off">
                <div class="nav-search-dropdown"></div>
            </div>
            <div class="nav-top-right">
                <button class="nav-burger" aria-label="Menu">☰</button>
            </div>
        </div>
        <nav class="nav-pages">
            <ul class="nav-links">
                ${navLinks.map(l => `<li><a href="${l.href}">${l.label}</a></li>`).join('\n                ')}
            </ul>
        </nav>
    `;

    document.body.insertBefore(header, document.body.firstChild);

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
