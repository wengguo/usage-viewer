(() => {
  'use strict';

  const byID = (id) => document.getElementById(id);
  const limitSelect = byID('limit-select');
  const statusRegion = byID('leaderboard-status');
  const grid = byID('leaderboard-grid');

  const windows = ['1d', '3d', '7d', '30d'];
  const sections = new Map(windows.map((window) => [window, document.querySelector(`[data-window="${window}"]`)]));

  const setStatus = (kind, title) => {
    statusRegion.className = 'status-region min-h-[3rem] py-3 text-sm text-slate-600 dark:text-slate-400';
    if (kind === 'error') {
      statusRegion.className += ' border-l-4 border-red-600 bg-red-50 px-4 text-red-700 dark:border-red-500 dark:bg-red-950/40 dark:text-red-400';
    }
    statusRegion.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    statusRegion.textContent = title;
  };

  const hasExactKeys = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === keys.length && keys.every((key) => Object.prototype.hasOwnProperty.call(value, key));
  const isCost = (value) => typeof value === 'string' && /^(0|[1-9]\d*)(\.\d+)?$/.test(value);
  const entryKeys = ['rank', 'keyMasked', 'name', 'groupName', 'actualCost'];
  const validEntry = (item) => hasExactKeys(item, entryKeys) &&
    typeof item.rank === 'number' && Number.isInteger(item.rank) && item.rank > 0 &&
    typeof item.keyMasked === 'string' && typeof item.name === 'string' &&
    typeof item.groupName === 'string' && isCost(item.actualCost);
  const validPayload = (payload) => hasExactKeys(payload, ['limit', 'windows']) &&
    typeof payload.limit === 'number' && Number.isInteger(payload.limit) &&
    payload.windows && typeof payload.windows === 'object' && !Array.isArray(payload.windows) &&
    windows.every((window) => Array.isArray(payload.windows[window]) && payload.windows[window].every(validEntry));

  const formatCost = (value) => {
    const num = +value;
    return Number.isFinite(num) ? num.toFixed(2) : value;
  };

  const renderWindow = (window, entries) => {
    const section = sections.get(window);
    const list = section.querySelector('[data-list]');
    const empty = section.querySelector('[data-empty]');
    while (list.firstChild) list.removeChild(list.firstChild);
    if (entries.length === 0) {
      empty.hidden = false;
      return;
    }
    empty.hidden = true;
    entries.forEach((entry) => {
      const item = document.createElement('li');
      item.className = 'flex items-center gap-3 rounded-md border border-slate-100 p-2 dark:border-slate-800';
      const rank = document.createElement('span');
      rank.className = 'flex h-6 w-6 flex-none items-center justify-center rounded-full bg-slate-100 text-xs font-semibold text-slate-600 dark:bg-slate-800 dark:text-slate-300';
      rank.textContent = String(entry.rank);
      const detail = document.createElement('div');
      detail.className = 'min-w-0 flex-1';
      const name = document.createElement('div');
      name.className = 'truncate text-sm font-semibold';
      name.textContent = entry.name;
      const meta = document.createElement('div');
      meta.className = 'truncate text-xs text-slate-500 dark:text-slate-400';
      meta.textContent = `${entry.groupName || '无分组'} · ${entry.keyMasked}`;
      detail.appendChild(name);
      detail.appendChild(meta);
      const cost = document.createElement('span');
      cost.className = 'flex-none text-sm font-semibold tabular-nums text-teal-700 dark:text-teal-400';
      cost.textContent = `$${formatCost(entry.actualCost)}`;
      item.appendChild(rank);
      item.appendChild(detail);
      item.appendChild(cost);
      list.appendChild(item);
    });
  };

  let requestSequence = 0;

  const loadLeaderboard = async () => {
    const limit = parseInt(limitSelect.value, 10) || 10;
    const sequence = ++requestSequence;
    setStatus('loading', '正在加载...');
    grid.classList.add('hidden');
    grid.classList.remove('grid');
    try {
      const response = await fetch('/api/leaderboard', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ limit }),
      });
      if (sequence !== requestSequence) return;
      if (!response.ok) throw new Error('leaderboard failed');
      const payload = await response.json();
      if (!validPayload(payload)) throw new Error('invalid response');
      windows.forEach((window) => renderWindow(window, payload.windows[window]));
      grid.classList.remove('hidden');
      grid.classList.add('grid');
      setStatus('ready', '');
    } catch (_) {
      setStatus('error', '排行榜加载失败，请重试');
    }
  };

  limitSelect.addEventListener('change', loadLeaderboard);
  loadLeaderboard();
})();
