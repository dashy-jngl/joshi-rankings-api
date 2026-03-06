// Burger menu toggle for mobile nav (two-level header)
(function() {
    function init() {
        const burger = document.querySelector('.nav-burger');
        const navPages = document.querySelector('.nav-pages');
        if (!burger || !navPages) return;

        burger.addEventListener('click', () => {
            navPages.classList.toggle('open');
        });

        // Close menu when clicking a link
        navPages.querySelectorAll('a').forEach(a => {
            a.addEventListener('click', () => navPages.classList.remove('open'));
        });

        // Set active nav link
        const path = window.location.pathname;
        navPages.querySelectorAll('.nav-links a').forEach(a => {
            const href = a.getAttribute('href');
            if (href === path || (href !== '/' && path.startsWith(href))) {
                a.classList.add('active');
            } else if (href === '/' && path === '/') {
                a.classList.add('active');
            }
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
