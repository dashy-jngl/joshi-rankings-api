// auth.js — Secret login system for Joshitori
(function() {
    'use strict';

    // --- Secret click trigger on nav brand ---
    let clickCount = 0;
    let clickTimer = null;
    const CLICKS_NEEDED = 3;
    const CLICK_WINDOW = 2000;

    const brand = document.querySelector('.nav-brand .brand-text')?.closest('a');
    if (brand) {
        // Use mousedown so we catch it before the link navigates
        brand.addEventListener('mousedown', function(e) {
            clickCount++;
            if (clickCount === 1) {
                clickTimer = setTimeout(() => { clickCount = 0; }, CLICK_WINDOW);
            }
            if (clickCount >= CLICKS_NEEDED) {
                e.preventDefault();
                clickCount = 0;
                clearTimeout(clickTimer);
                showAuthModal();
            }
        });
        // Prevent navigation on 2nd+ click within the window
        brand.addEventListener('click', function(e) {
            if (clickCount > 0) {
                e.preventDefault();
            }
        });
    }

    // --- Auth state (exposed globally for other pages) ---
    let currentUser = null;
    window.joshiAuth = { getUser: () => currentUser };

    // --- Modal HTML ---
    function createModal() {
        if (document.getElementById('joshi-auth-modal')) return;
        const modal = document.createElement('div');
        modal.id = 'joshi-auth-modal';
        modal.innerHTML = `
            <div class="auth-overlay"></div>
            <div class="auth-dialog">
                <button class="auth-close">&times;</button>
                <div id="auth-login-form">
                    <h2>🏆 Login</h2>
                    <input type="text" id="auth-username" placeholder="Username" autocomplete="username">
                    <input type="password" id="auth-password" placeholder="Password" autocomplete="current-password">
                    <button id="auth-submit" class="auth-btn">Login</button>
                    <div id="auth-error" class="auth-error"></div>
                </div>
                <div id="auth-setup-form" style="display:none">
                    <h2>🏆 Initial Setup</h2>
                    <p class="auth-hint">No admin account exists. Create one to get started.</p>
                    <input type="text" id="setup-username" placeholder="Username" autocomplete="username">
                    <input type="password" id="setup-password" placeholder="Password" autocomplete="new-password">
                    <button id="setup-submit" class="auth-btn">Create Admin</button>
                    <div id="setup-error" class="auth-error"></div>
                </div>
            </div>
        `;
        document.body.appendChild(modal);

        // Styles
        const style = document.createElement('style');
        style.textContent = `
            #joshi-auth-modal { position:fixed; inset:0; z-index:10000; display:flex; align-items:center; justify-content:center; }
            #joshi-auth-modal .auth-overlay { position:absolute; inset:0; background:rgba(0,0,0,0.7); }
            #joshi-auth-modal .auth-dialog { position:relative; background:#1a1a2e; border:1px solid #e91e63; border-radius:12px; padding:32px; width:340px; max-width:90vw; }
            #joshi-auth-modal .auth-close { position:absolute; top:10px; right:14px; background:none; border:none; color:#888; font-size:1.4rem; cursor:pointer; }
            #joshi-auth-modal .auth-close:hover { color:#fff; }
            #joshi-auth-modal h2 { color:#e91e63; margin-bottom:20px; font-size:1.2rem; text-align:center; }
            #joshi-auth-modal input { display:block; width:100%; padding:10px 12px; margin-bottom:12px; background:#111; border:1px solid #333; border-radius:6px; color:#fff; font-size:0.95rem; }
            #joshi-auth-modal input:focus { outline:none; border-color:#e91e63; }
            #joshi-auth-modal .auth-btn { display:block; width:100%; padding:10px; background:#e91e63; color:#fff; border:none; border-radius:6px; font-size:0.95rem; font-weight:600; cursor:pointer; }
            #joshi-auth-modal .auth-btn:hover { background:#c2185b; }
            #joshi-auth-modal .auth-error { color:#f44336; font-size:0.85rem; margin-top:10px; text-align:center; min-height:1.2em; }
            #joshi-auth-modal .auth-hint { color:#888; font-size:0.85rem; margin-bottom:16px; text-align:center; }
            .auth-user-indicator { display:flex; align-items:center; gap:8px; font-size:0.85rem; color:#ccc; margin-left:12px; }
            .auth-user-indicator a { color:#e91e63; text-decoration:none; font-size:0.8rem; }
            .auth-user-indicator a:hover { text-decoration:underline; }
        `;
        document.head.appendChild(style);

        // Events
        modal.querySelector('.auth-overlay').addEventListener('click', hideAuthModal);
        modal.querySelector('.auth-close').addEventListener('click', hideAuthModal);
        modal.querySelector('#auth-submit').addEventListener('click', doLogin);
        modal.querySelector('#setup-submit').addEventListener('click', doSetup);
        modal.querySelector('#auth-password').addEventListener('keydown', e => { if (e.key === 'Enter') doLogin(); });
        modal.querySelector('#setup-password').addEventListener('keydown', e => { if (e.key === 'Enter') doSetup(); });
    }

    async function showAuthModal() {
        createModal();
        const modal = document.getElementById('joshi-auth-modal');
        modal.style.display = 'flex';

        // Check if setup is needed
        try {
            const resp = await fetch('/api/auth/me', { credentials: 'include' });
            if (resp.ok) {
                // Already logged in
                hideAuthModal();
                return;
            }
        } catch(e) {}

        // Check if any users exist by trying setup availability
        try {
            const resp = await fetch('/api/auth/setup', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username: '', password: '' })
            });
            const data = await resp.json();
            if (data.error === 'Setup already completed') {
                document.getElementById('auth-login-form').style.display = '';
                document.getElementById('auth-setup-form').style.display = 'none';
            } else {
                // Setup needed (bad request means endpoint is available)
                document.getElementById('auth-login-form').style.display = 'none';
                document.getElementById('auth-setup-form').style.display = '';
            }
        } catch(e) {
            document.getElementById('auth-login-form').style.display = '';
            document.getElementById('auth-setup-form').style.display = 'none';
        }

        document.getElementById('auth-error').textContent = '';
        document.getElementById('setup-error').textContent = '';
    }

    function hideAuthModal() {
        const modal = document.getElementById('joshi-auth-modal');
        if (modal) modal.style.display = 'none';
    }

    async function doLogin() {
        const username = document.getElementById('auth-username').value.trim();
        const password = document.getElementById('auth-password').value;
        const errEl = document.getElementById('auth-error');
        errEl.textContent = '';

        if (!username || !password) { errEl.textContent = 'Fill in both fields'; return; }

        try {
            const resp = await fetch('/api/auth/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                credentials: 'include',
                body: JSON.stringify({ username, password })
            });
            const data = await resp.json();
            if (!resp.ok) { errEl.textContent = data.error || 'Login failed'; return; }

            localStorage.setItem('joshi_token', data.token);
            currentUser = data.user;
            hideAuthModal();
            updateAuthUI();
        } catch(e) {
            errEl.textContent = 'Connection error';
        }
    }

    async function doSetup() {
        const username = document.getElementById('setup-username').value.trim();
        const password = document.getElementById('setup-password').value;
        const errEl = document.getElementById('setup-error');
        errEl.textContent = '';

        if (!username || !password) { errEl.textContent = 'Fill in both fields'; return; }

        try {
            const resp = await fetch('/api/auth/setup', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username, password })
            });
            const data = await resp.json();
            if (!resp.ok) { errEl.textContent = data.error || 'Setup failed'; return; }

            // Auto-login after setup
            await doLoginWith(username, password);
        } catch(e) {
            errEl.textContent = 'Connection error';
        }
    }

    async function doLoginWith(username, password) {
        const resp = await fetch('/api/auth/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ username, password })
        });
        const data = await resp.json();
        if (resp.ok) {
            localStorage.setItem('joshi_token', data.token);
            currentUser = data.user;
            hideAuthModal();
            updateAuthUI();
        }
    }

    async function doLogout() {
        await fetch('/api/auth/logout', { method: 'POST', credentials: 'include' });
        localStorage.removeItem('joshi_token');
        currentUser = null;
        updateAuthUI();
    }

    function updateAuthUI() {
        // Remove existing indicator
        const existing = document.querySelector('.auth-user-indicator');
        if (existing) existing.remove();

        // Dispatch event for other scripts
        window.dispatchEvent(new CustomEvent('joshi-auth-change', { detail: currentUser }));

        if (!currentUser) return;

        const navRight = document.querySelector('.nav-top-right');
        if (!navRight) return;

        const indicator = document.createElement('div');
        indicator.className = 'auth-user-indicator';

        let html = `<span>${currentUser.display_name || currentUser.username}</span>`;
        if (currentUser.role === 'admin') {
            html += `<a href="/admin">Admin</a>`;
        }
        html += `<a href="#" id="auth-logout">Logout</a>`;
        indicator.innerHTML = html;
        navRight.insertBefore(indicator, navRight.firstChild);

        document.getElementById('auth-logout').addEventListener('click', function(e) {
            e.preventDefault();
            doLogout();
        });
    }

    // --- Check auth on page load ---
    async function checkAuth() {
        try {
            const resp = await fetch('/api/auth/me', { credentials: 'include' });
            if (resp.ok) {
                const data = await resp.json();
                currentUser = { username: data.username, role: data.role, display_name: data.username };
                updateAuthUI();
            }
        } catch(e) {}
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', checkAuth);
    } else {
        checkAuth();
    }
})();
