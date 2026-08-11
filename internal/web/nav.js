(() => {
  'use strict';

  const protectedLinks = document.querySelectorAll('[data-nav-auth]');
  const authControl = document.getElementById('nav-auth-control');

  const renderLoggedOut = () => {
    protectedLinks.forEach((link) => link.classList.add('hidden'));
    if (!authControl) return;
    authControl.textContent = '';
    const link = document.createElement('a');
    link.href = '/login';
    link.className = 'flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800';
    link.textContent = '登录';
    authControl.appendChild(link);
  };

  const renderLoggedIn = () => {
    protectedLinks.forEach((link) => link.classList.remove('hidden'));
    if (!authControl) return;
    authControl.textContent = '';
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800';
    button.textContent = '退出登录';
    button.addEventListener('click', async () => {
      try {
        await fetch('/api/logout', { method: 'POST' });
      } catch (_) {
        // Ignore network failure; navigating to / will re-check auth status.
      }
      window.location.assign('/');
    });
    authControl.appendChild(button);
  };

  fetch('/api/auth/status')
    .then((response) => (response.ok ? response.json() : { authenticated: false }))
    .then((payload) => (payload && payload.authenticated ? renderLoggedIn() : renderLoggedOut()))
    .catch(() => renderLoggedOut());
})();
