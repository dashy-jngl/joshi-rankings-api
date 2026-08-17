// WrestlerSearch — reusable wrestler autocomplete component.
//
// Usage:
//   WrestlerSearch.attach(inputEl, {
//       source: 'names' | 'slim',        // 'names' = id/name/promotion, 'slim' adds elo
//       minChars: 2,
//       detail: w => 'subtitle html',    // right-hand subtitle per item
//       exclude: w => bool,              // hide items (e.g. already selected)
//       onSelect: w => {},               // required for custom behavior;
//                                        // default navigates to /wrestler/:id
//       clearOnSelect: true,             // false = put picked name in the input
//   });
//
// Provides consistent behavior everywhere: 150ms debounce, exact/prefix/contains
// ranking, match highlighting, ArrowUp/ArrowDown/Enter/Escape keyboard support,
// and click-outside to close. The dropdown element is created automatically
// inside the input's parent (which gets position:relative via .ws-anchor).
(function() {
    const ENDPOINTS = {
        names: '/api/wrestler-names',
        slim: '/api/wrestler-slim',
    };
    const cache = {};

    // Shared, deduplicated fetch of the wrestler list (also usable by pages
    // that need the raw list — returns a promise).
    function load(source) {
        source = source || 'names';
        if (!cache[source]) {
            cache[source] = fetch(ENDPOINTS[source])
                .then(r => r.json())
                .catch(() => []);
        }
        return cache[source];
    }

    function rank(list, q, exclude) {
        return list.filter(w =>
            (w.name.toLowerCase().includes(q) ||
             (w.promotion || '').toLowerCase().includes(q)) &&
            !(exclude && exclude(w))
        ).sort((a, b) => {
            const an = a.name.toLowerCase(), bn = b.name.toLowerCase();
            const aExact = an === q, bExact = bn === q;
            if (aExact !== bExact) return aExact ? -1 : 1;
            const aStarts = an.startsWith(q), bStarts = bn.startsWith(q);
            if (aStarts !== bStarts) return aStarts ? -1 : 1;
            return an.localeCompare(bn);
        }).slice(0, 10);
    }

    function escapeHTML(s) {
        return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
            .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    function highlight(text, q) {
        const i = text.toLowerCase().indexOf(q);
        if (i === -1) return escapeHTML(text);
        return escapeHTML(text.slice(0, i)) +
            '<mark>' + escapeHTML(text.slice(i, i + q.length)) + '</mark>' +
            escapeHTML(text.slice(i + q.length));
    }

    function attach(input, opts) {
        opts = opts || {};
        const source = opts.source || 'names';
        const minChars = opts.minChars != null ? opts.minChars : 2;
        const detail = opts.detail || (w => escapeHTML(w.promotion || 'Freelance'));
        const onSelect = opts.onSelect || (w => { window.location.href = '/wrestler/' + w.id; });
        const clearOnSelect = opts.clearOnSelect !== false;

        const anchor = input.parentElement;
        anchor.classList.add('ws-anchor');
        let dropdown = anchor.querySelector('.ws-dropdown');
        if (!dropdown) {
            dropdown = document.createElement('div');
            dropdown.className = 'ws-dropdown';
            anchor.appendChild(dropdown);
        }

        let debounceTimer = null;
        load(source); // preload so first search is instant

        function close() {
            dropdown.style.display = 'none';
        }

        function select(w) {
            input.value = clearOnSelect ? '' : w.name;
            close();
            onSelect(w);
        }

        async function doSearch() {
            const q = input.value.trim().toLowerCase();
            if (q.length < minChars) { close(); return; }
            const list = await load(source);
            const matches = rank(list, q, opts.exclude);

            if (matches.length === 0) {
                dropdown.innerHTML = '<div class="ws-empty">No results</div>';
            } else {
                dropdown.innerHTML = matches.map((w, i) =>
                    `<div class="ws-item" data-idx="${i}">
                        <span class="ws-name">${highlight(w.name, q)}</span>
                        <span class="ws-sub">${detail(w)}</span>
                    </div>`
                ).join('');
                dropdown.querySelectorAll('.ws-item').forEach(el => {
                    el.addEventListener('click', () => select(matches[el.dataset.idx]));
                });
            }
            dropdown.style.display = 'block';
        }

        input.addEventListener('input', () => {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(doSearch, 150);
        });

        input.addEventListener('focus', () => {
            if (input.value.trim().length >= minChars) doSearch();
        });

        input.addEventListener('keydown', (e) => {
            const items = dropdown.querySelectorAll('.ws-item');
            const active = dropdown.querySelector('.ws-item.active');
            let idx = Array.from(items).indexOf(active);
            if (e.key === 'ArrowDown') {
                e.preventDefault();
                if (active) active.classList.remove('active');
                idx = Math.min(idx + 1, items.length - 1);
                if (items[idx]) {
                    items[idx].classList.add('active');
                    items[idx].scrollIntoView({ block: 'nearest' });
                }
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                if (active) active.classList.remove('active');
                idx = Math.max(idx - 1, 0);
                if (items[idx]) {
                    items[idx].classList.add('active');
                    items[idx].scrollIntoView({ block: 'nearest' });
                }
            } else if (e.key === 'Enter') {
                e.preventDefault();
                const sel = active || items[0];
                if (sel) sel.click();
            } else if (e.key === 'Escape') {
                close();
                input.blur();
            }
        });

        document.addEventListener('click', (e) => {
            if (!anchor.contains(e.target)) close();
        });

        return { close, refresh: doSearch };
    }

    window.WrestlerSearch = { attach, load };
})();
