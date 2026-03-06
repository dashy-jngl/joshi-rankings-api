// Global nav search — autocomplete wrestler lookup
(function() {
    let wrestlers = null;
    let debounceTimer = null;

    async function getWrestlers() {
        if (wrestlers) return wrestlers;
        try {
            // Use slim endpoint — only id/name/promotion, no aliases
            wrestlers = await fetch('/api/wrestler-names').then(r => r.json());
        } catch(e) {
            wrestlers = [];
        }
        return wrestlers;
    }

    function init() {
        const wrap = document.getElementById('nav-search-wrap');
        if (!wrap) return;
        const input = wrap.querySelector('input');
        const dropdown = wrap.querySelector('.nav-search-dropdown');

        // Preload wrestler list immediately so first search is instant
        getWrestlers();

        input.addEventListener('input', () => {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(() => doSearch(input, dropdown), 150);
        });

        input.addEventListener('focus', () => {
            if (input.value.length >= 2) doSearch(input, dropdown);
        });

        document.addEventListener('click', (e) => {
            if (!wrap.contains(e.target)) dropdown.style.display = 'none';
        });

        input.addEventListener('keydown', (e) => {
            const items = dropdown.querySelectorAll('.nav-search-item');
            const active = dropdown.querySelector('.nav-search-item.active');
            let idx = Array.from(items).indexOf(active);
            if (e.key === 'ArrowDown') {
                e.preventDefault();
                if (active) active.classList.remove('active');
                idx = Math.min(idx + 1, items.length - 1);
                items[idx]?.classList.add('active');
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                if (active) active.classList.remove('active');
                idx = Math.max(idx - 1, 0);
                items[idx]?.classList.add('active');
            } else if (e.key === 'Enter') {
                e.preventDefault();
                const sel = dropdown.querySelector('.nav-search-item.active') || items[0];
                if (sel) sel.click();
            } else if (e.key === 'Escape') {
                dropdown.style.display = 'none';
                input.blur();
            }
        });
    }

    async function doSearch(input, dropdown) {
        const q = input.value.trim().toLowerCase();
        if (q.length < 2) { dropdown.style.display = 'none'; return; }
        const list = await getWrestlers();
        const matches = list.filter(w =>
            w.name.toLowerCase().includes(q) ||
            (w.promotion || '').toLowerCase().includes(q)
        ).sort((a, b) => {
            const an = a.name.toLowerCase(), bn = b.name.toLowerCase();
            const aExact = an === q, bExact = bn === q;
            if (aExact !== bExact) return aExact ? -1 : 1;
            const aStarts = an.startsWith(q), bStarts = bn.startsWith(q);
            if (aStarts !== bStarts) return aStarts ? -1 : 1;
            return an.localeCompare(bn);
        }).slice(0, 10);

        if (matches.length === 0) {
            dropdown.innerHTML = '<div class="nav-search-empty">No results</div>';
        } else {
            dropdown.innerHTML = matches.map(w =>
                `<div class="nav-search-item" data-id="${w.id}">
                    <span class="nav-search-name">${highlight(w.name, q)}</span>
                    <span class="nav-search-promo">${w.promotion || 'Freelance'}</span>
                </div>`
            ).join('');
            dropdown.querySelectorAll('.nav-search-item').forEach(el => {
                el.addEventListener('click', () => {
                    window.location.href = '/wrestler/' + el.dataset.id;
                });
            });
        }
        dropdown.style.display = 'block';
    }

    function highlight(text, q) {
        const i = text.toLowerCase().indexOf(q);
        if (i === -1) return text;
        return text.slice(0, i) + '<mark>' + text.slice(i, i + q.length) + '</mark>' + text.slice(i + q.length);
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
