// Global nav search — wires the shared WrestlerSearch component to the nav bar.
// All behavior (debounce, ranking, keyboard nav, highlighting) lives in
// js/wrestler-search.js so every search box on the site acts the same.
(function() {
    function init() {
        const wrap = document.getElementById('nav-search-wrap');
        if (!wrap || !window.WrestlerSearch) return;
        WrestlerSearch.attach(wrap.querySelector('input'), {
            source: 'names',
            onSelect: w => { window.location.href = '/wrestler/' + w.id; },
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
