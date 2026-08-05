(() => {
  'use strict';

  const form = document.getElementById('cred-form');
  const button = document.getElementById('connect-button');
  const buttonLabel = document.getElementById('button-label');
  const spinner = document.getElementById('spinner');
  const status = document.getElementById('status');
  const hostInput = document.getElementById('host');
  const portInput = document.getElementById('port');
  const userInput = document.getElementById('user');
  const passwordInput = document.getElementById('password');
  const dbnameInput = document.getElementById('dbname');
  const sslmodeInput = document.getElementById('sslmode');

  const setBusy = (busy) => {
    button.disabled = busy;
    spinner.hidden = !busy;
    buttonLabel.textContent = busy ? 'Connecting...' : 'Connect';
  };

  const setStatus = (kind, message) => {
    status.className = 'status-region';
    if (kind === 'error') status.classList.add('error');
    if (kind === 'success') status.classList.add('success');
    status.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    status.textContent = message;
  };

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    setBusy(true);
    setStatus('idle', '');

    const port = parseInt(portInput.value, 10);
    const payload = {
      host: hostInput.value.trim(),
      port: isNaN(port) || port < 1 || port > 65535 ? 5432 : port,
      user: userInput.value.trim(),
      password: passwordInput.value,
      dbname: dbnameInput.value.trim(),
      sslmode: sslmodeInput.value,
    };

    if (!payload.host || !payload.user || !payload.dbname) {
      setStatus('error', 'Host, user, and database name are required.');
      setBusy(false);
      return;
    }

    try {
      const response = await fetch('/api/connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const result = await response.json();
      if (result.success) {
        setStatus('success', 'Connected! Redirecting...');
        setTimeout(() => { window.location.href = '/'; }, 800);
      } else {
        setStatus('error', result.message || 'Connection failed.');
        passwordInput.focus();
        passwordInput.select();
      }
    } catch (_) {
      setStatus('error', 'Request failed. Check the viewer service and try again.');
    } finally {
      setBusy(false);
    }
  });

  // Pre-fill fields from discovered credentials (host/user/dbname from config,
  // saved file, or env). The password is never returned and stays empty.
  const prefilled = { host: '', port: '5432', user: '', dbname: '', sslmode: 'disable' };
  fetch('/api/creds/status')
    .then((response) => response.json())
    .then((status) => {
      if (status.host) prefilled.host = status.host;
      if (status.port) prefilled.port = String(status.port);
      if (status.user) prefilled.user = status.user;
      if (status.dbname) prefilled.dbname = status.dbname;
      if (status.sslmode) prefilled.sslmode = status.sslmode;
      hostInput.value = hostInput.value || prefilled.host;
      portInput.value = portInput.value || prefilled.port;
      userInput.value = userInput.value || prefilled.user;
      dbnameInput.value = dbnameInput.value || prefilled.dbname;
      sslmodeInput.value = prefilled.sslmode;
      if (!passwordInput.value) passwordInput.focus();
    })
    .catch(() => {
      hostInput.focus();
    });

  // Focus the first empty required field.
  if (!hostInput.value) hostInput.focus();
  else if (!userInput.value) userInput.focus();
  else if (!passwordInput.value) passwordInput.focus();
  else if (!dbnameInput.value) dbnameInput.focus();
})();
