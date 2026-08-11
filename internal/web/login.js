(() => {
  'use strict';

  const byID = (id) => document.getElementById(id);
  const form = byID('login-form');
  const usernameInput = byID('username');
  const passwordInput = byID('password');
  const button = byID('login-button');
  const spinner = byID('login-spinner');
  const statusRegion = byID('login-status');

  const setStatus = (kind, title) => {
    statusRegion.className = 'status-region min-h-[2.5rem] py-3 text-sm text-slate-600 dark:text-slate-400';
    if (kind === 'error') {
      statusRegion.className += ' border-l-4 border-red-600 bg-red-50 px-4 text-red-700 dark:border-red-500 dark:bg-red-950/40 dark:text-red-400';
    }
    statusRegion.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    statusRegion.textContent = title;
  };

  const setBusy = (busy) => {
    usernameInput.disabled = busy;
    passwordInput.disabled = busy;
    button.disabled = busy;
    spinner.hidden = !busy;
  };

  const safeNext = () => {
    const params = new URLSearchParams(window.location.search);
    const next = params.get('next') || '';
    if (!next.startsWith('/') || next.startsWith('//')) return '/';
    return next;
  };

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    setBusy(true);
    setStatus('loading', '正在登录...');
    try {
      const response = await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: usernameInput.value, password: passwordInput.value }),
      });
      if (!response.ok) {
        setStatus('error', '账号或密码错误');
        setBusy(false);
        return;
      }
      window.location.assign(safeNext());
    } catch (_) {
      setStatus('error', '登录失败，请重试');
      setBusy(false);
    }
  });
})();
