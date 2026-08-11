(() => {
  'use strict';

  const byID = (id) => document.getElementById(id);
  const form = byID('self-form');
  const credentialInput = byID('credential');
  const button = byID('self-button');
  const spinner = byID('self-spinner');
  const statusRegion = byID('self-status');
  const placeholder = byID('self-placeholder');
  const card = byID('self-card');
  const nameEl = byID('self-name');
  const groupEl = byID('self-group');
  const statusBadge = byID('self-status-badge');
  const keyEl = byID('self-key');
  const todayCostEl = byID('self-today-cost');
  const quotaEl = byID('self-quota');
  const expiresEl = byID('self-expires');
  const quotaPercentEl = byID('self-quota-percent');
  const quotaBarEl = byID('self-quota-bar');
  const chartWrap = byID('self-chart-wrap');
  const chartTitleEl = byID('self-chart-title');
  const chartDaysSelect = byID('self-chart-days');

  let lastDailyUsage = [];

  const setStatus = (kind, title) => {
    statusRegion.className = 'status-region min-h-[3rem] py-3 text-sm text-slate-600 dark:text-slate-400';
    if (kind === 'error') {
      statusRegion.className += ' border-l-4 border-red-600 bg-red-50 px-4 text-red-700 dark:border-red-500 dark:bg-red-950/40 dark:text-red-400';
    }
    statusRegion.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    statusRegion.textContent = title;
  };

  const setBusy = (busy) => {
    credentialInput.disabled = busy;
    button.disabled = busy;
    spinner.hidden = !busy;
  };

  const validateCredential = (rawValue) => {
    const value = rawValue.trim();
    const length = Array.from(value).length;
    if (length < 2) return { error: '至少输入 2 个字符' };
    if (length > 100) return { error: '不能超过 100 个字符' };
    return { value };
  };

  const hasExactKeys = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === keys.length && keys.every((key) => Object.prototype.hasOwnProperty.call(value, key));
  const isCost = (value) => typeof value === 'string' && /^(0|[1-9]\d*)(\.\d+)?$/.test(value);
  const dailyPointKeys = ['date', 'actualCost'];
  const validDailyPoint = (point) => hasExactKeys(point, dailyPointKeys) && typeof point.date === 'string' && isCost(point.actualCost);
  const resultKeys = ['keyMasked', 'name', 'groupName', 'quota', 'quotaUsed', 'status', 'expiresAt', 'todayCost', 'dailyUsage'];
  const validResult = (payload) => hasExactKeys(payload, resultKeys) &&
    typeof payload.keyMasked === 'string' && typeof payload.name === 'string' && typeof payload.groupName === 'string' &&
    isCost(payload.quota) && isCost(payload.quotaUsed) && typeof payload.status === 'string' &&
    typeof payload.expiresAt === 'string' && isCost(payload.todayCost) &&
    Array.isArray(payload.dailyUsage) && payload.dailyUsage.every(validDailyPoint);

  const formatCost = (value) => {
    const num = +value;
    return Number.isFinite(num) ? num.toFixed(4) : value;
  };
  const formatQuota = (value) => {
    if (!value || value === '0') return '无限制';
    const num = +value;
    return Number.isFinite(num) ? num.toFixed(2) : value;
  };
  const formatTimestamp = (value) => {
    if (!value) return '—';
    try { return new Date(value).toLocaleString(); } catch (_) { return value; }
  };

  const statusBadgeClass = (status) => status === 'active'
    ? 'rounded-full bg-emerald-50 px-3 py-1 text-xs font-semibold text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    : 'rounded-full bg-amber-50 px-3 py-1 text-xs font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-300';

  const renderCard = (result) => {
    nameEl.textContent = result.name;
    groupEl.textContent = result.groupName || '无分组';
    statusBadge.className = statusBadgeClass(result.status);
    statusBadge.textContent = result.status;
    keyEl.textContent = result.keyMasked;
    todayCostEl.textContent = `$${formatCost(result.todayCost)}`;
    quotaEl.textContent = `${formatCost(result.quotaUsed)} / ${formatQuota(result.quota)}`;
    expiresEl.textContent = formatTimestamp(result.expiresAt);

    const quota = +result.quota || 0;
    const quotaUsed = +result.quotaUsed || 0;
    const percent = quota > 0 ? Math.min(100, (quotaUsed / quota) * 100) : 0;
    quotaPercentEl.textContent = quota > 0 ? `${percent.toFixed(1)}%` : '无限制';
    quotaBarEl.style.width = `${percent}%`;

    lastDailyUsage = result.dailyUsage;
    renderChartForSelectedDays();
    placeholder.classList.add('hidden');
    card.hidden = false;
  };

  const cutoffDate = (days) => {
    const now = new Date();
    const cutoff = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() - (days - 1)));
    return cutoff.toISOString().slice(0, 10);
  };

  const renderChartForSelectedDays = () => {
    const days = +chartDaysSelect.value || 30;
    chartTitleEl.textContent = `近 ${days} 天用量`;
    const cutoff = cutoffDate(days);
    renderChart(lastDailyUsage.filter((point) => point.date >= cutoff));
  };

  const chartPalette = () => document.documentElement.classList.contains('dark')
    ? { grid: '#334155', axisText: '#94a3b8', line: '#2dd4bf', point: '#2dd4bf' }
    : { grid: '#e9eaeb', axisText: '#52606d', line: '#0f766e', point: '#0f766e' };

  const renderChart = (items) => {
    chartWrap.textContent = '';
    if (items.length === 0) return;
    const palette = chartPalette();
    const width = 640;
    const height = 200;
    const padL = 48;
    const padR = 12;
    const padT = 12;
    const padB = 28;
    const plotW = width - padL - padR;
    const plotH = height - padT - padB;
    const maxCost = Math.max(...items.map((item) => +item.actualCost || 0), 0.001);
    const x = (i) => padL + (items.length === 1 ? plotW / 2 : (i / (items.length - 1)) * plotW);
    const y = (value) => padT + plotH - (Math.min(value, maxCost) / maxCost) * plotH;

    const svgNs = 'http' + '://www.w3.org/2000/svg';
    const svg = document.createElementNS(svgNs, 'svg');
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', '用量折线图');

    const ySteps = 3;
    for (let step = 0; step <= ySteps; step++) {
      const value = (maxCost * step) / ySteps;
      const yy = y(value);
      const line = document.createElementNS(svgNs, 'line');
      line.setAttribute('x1', String(padL));
      line.setAttribute('y1', String(yy));
      line.setAttribute('x2', String(width - padR));
      line.setAttribute('y2', String(yy));
      line.setAttribute('stroke', palette.grid);
      line.setAttribute('stroke-width', '1');
      svg.appendChild(line);
      const label = document.createElementNS(svgNs, 'text');
      label.setAttribute('x', String(padL - 6));
      label.setAttribute('y', String(yy + 4));
      label.setAttribute('text-anchor', 'end');
      label.setAttribute('font-size', '10');
      label.setAttribute('fill', palette.axisText);
      label.textContent = value.toFixed(2);
      svg.appendChild(label);
    }

    const labelIndexes = [0, Math.floor((items.length - 1) / 2), items.length - 1];
    labelIndexes.forEach((i) => {
      const text = document.createElementNS(svgNs, 'text');
      text.setAttribute('x', String(x(i)));
      text.setAttribute('y', String(height - padB + 16));
      text.setAttribute('text-anchor', 'middle');
      text.setAttribute('font-size', '10');
      text.setAttribute('fill', palette.axisText);
      text.textContent = items[i].date;
      svg.appendChild(text);
    });

    const points = items.map((item, i) => `${x(i)},${y(+item.actualCost || 0)}`);
    const polyline = document.createElementNS(svgNs, 'polyline');
    polyline.setAttribute('points', points.join(' '));
    polyline.setAttribute('fill', 'none');
    polyline.setAttribute('stroke', palette.line);
    polyline.setAttribute('stroke-width', '2');
    polyline.setAttribute('stroke-linejoin', 'round');
    polyline.setAttribute('stroke-linecap', 'round');
    svg.appendChild(polyline);

    items.forEach((item, i) => {
      const circle = document.createElementNS(svgNs, 'circle');
      circle.setAttribute('cx', String(x(i)));
      circle.setAttribute('cy', String(y(+item.actualCost || 0)));
      circle.setAttribute('r', '3');
      circle.setAttribute('fill', palette.point);
      svg.appendChild(circle);
    });

    chartWrap.appendChild(svg);
  };

  let requestSequence = 0;

  const submitLookup = async (credential) => {
    const sequence = ++requestSequence;
    setBusy(true);
    setStatus('loading', '正在查询...');
    card.hidden = true;
    placeholder.classList.add('hidden');
    try {
      const response = await fetch('/api/self-lookup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ credential }),
      });
      if (sequence !== requestSequence) return;
      if (response.status === 404) {
        setStatus('empty', '未找到匹配的 Key');
        placeholder.classList.remove('hidden');
        return;
      }
      if (!response.ok) throw new Error('self-lookup failed');
      const payload = await response.json();
      if (!validResult(payload)) throw new Error('invalid response');
      renderCard(payload);
      setStatus('ready', '');
    } catch (_) {
      setStatus('error', '查询失败，请重试');
      placeholder.classList.remove('hidden');
    } finally {
      if (sequence === requestSequence) setBusy(false);
    }
  };

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const validation = validateCredential(credentialInput.value);
    if (validation.error) {
      credentialInput.setAttribute('aria-invalid', 'true');
      setStatus('error', validation.error);
      credentialInput.focus();
      return;
    }
    credentialInput.removeAttribute('aria-invalid');
    submitLookup(validation.value);
  });

  chartDaysSelect.addEventListener('change', renderChartForSelectedDays);
})();
