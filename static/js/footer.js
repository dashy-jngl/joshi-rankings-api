// Shared footer — injected on all pages
(function() {
    const footer = document.createElement('footer');
    footer.className = 'site-footer';
    footer.innerHTML = `
        <div class="footer-content" style="position:relative;max-width:1200px;margin:0 auto;text-align:center;">
            <p>Match data sourced from <a href="https://www.cagematch.net" target="_blank" rel="noopener">Cagematch.net</a> — thank you for the incredible database ❤️</p>
            <p class="footer-sub">Joshitori is a fan project and is not affiliated with Cagematch.</p>
            <a href="/contact" style="position:absolute;right:0;top:50%;transform:translateY(-50%);">📬 Contact</a>
        </div>
    `;
    document.body.appendChild(footer);
})();
