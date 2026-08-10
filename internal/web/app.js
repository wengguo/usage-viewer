(() => {
  'use strict';

  const state = {
    searchSequence: 0, searchController: null, results: [], query: '', page: 1,
    pageSize: 20, total: 0, sortBy: 'id', sortDirection: 'desc'
  };

  const byID = (id) => document.getElementById(id);
  const searchForm = byID('search-form');
  const queryInput = byID('query');
  const searchButton = byID('search-button');
  const searchSpinner = byID('search-spinner');
  const searchStatus = byID('search-status');
  const resultsRegion = byID('results');
  const resultCount = byID('result-count');
  const keyBody = byID('key-body');
  const keyCards = byID('key-cards');
  const todaySortButton = byID('sort-today-cost');
  const total30dSortButton = byID('sort-total-30d-cost');
  const todaySortHeader = byID('today-cost-header');
  const total30dSortHeader = byID('total-30d-cost-header');
  const previousPageButton = byID('previous-page');
  const nextPageButton = byID('next-page');
  const pageStatus = byID('page-status');
  const usageDialog = byID('usage-dialog');
  const dialogTitle = byID('dialog-title');
  const dialogClose = byID('dialog-close');
  const dialogDays = byID('dialog-days');
  const chartWrap = byID('chart-wrap');
  const dailyBody = byID('daily-body');
  const dialogStatus = byID('dialog-status');
  let currentKeyId = 0;

  const setStatus = (region, kind, title, detail = '') => {
    region.className = 'status-region min-h-[3rem] py-3 text-sm text-slate-600 dark:text-slate-400';
    if (kind === 'error') region.className += ' border-l-4 border-red-600 bg-red-50 px-4 text-red-700 dark:border-red-500 dark:bg-red-950/40 dark:text-red-400';
    region.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    region.textContent = '';
    if (!title) return;
    const heading = document.createElement('div');
    heading.className = 'status-title font-semibold';
    heading.textContent = title;
    region.appendChild(heading);
    if (detail) {
      const body = document.createElement('div');
      body.className = 'status-detail mt-1';
      body.textContent = detail;
      region.appendChild(body);
    }
  };

  const setSearchBusy = (busy) => {
    queryInput.disabled = busy;
    searchButton.disabled = busy;
    todaySortButton.disabled = busy;
    total30dSortButton.disabled = busy;
    previousPageButton.disabled = busy || state.page <= 1;
    nextPageButton.disabled = busy || state.page * state.pageSize >= state.total;
    searchSpinner.hidden = !busy;
    resultsRegion.setAttribute('aria-busy', String(busy));
  };

  const clearRows = () => {
    while (keyBody.firstChild) keyBody.removeChild(keyBody.firstChild);
    while (keyCards.firstChild) keyCards.removeChild(keyCards.firstChild);
  };

  const clearDisplayedResults = () => {
    state.results = [];
    resultCount.textContent = '';
    clearRows();
    resultsRegion.hidden = true;
  };

  const validateSearch = (rawValue) => {
    const value = rawValue.trim();
    if (!value) return { value: '' };
    const length = Array.from(value).length;
    if (length < 2) return { error: '至少输入 2 个字符' };
    if (length > 100) return { error: '搜索内容不能超过 100 个字符' };
    return { value };
  };

  const hasExactKeys = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === keys.length && keys.every((key) => Object.prototype.hasOwnProperty.call(value, key));
  const isPositiveID = (value) => typeof value === 'number' && Number.isInteger(value) && value > 0;
  const isCost = (value) => typeof value === 'string' && /^(0|[1-9]\d*)(\.\d+)?$/.test(value);
  const isConcurrency = (value) => typeof value === 'number' && Number.isInteger(value) && value >= 0;
  const isTimestamp = (value) => typeof value === 'string' && Number.isFinite(Date.parse(value));

  const keyResultKeys = ['id', 'name', 'groupName', 'currentConcurrency', 'todayCost', 'total30dCost', 'quota', 'quotaUsed', 'lastUsedAt', 'expiresAt', 'status', 'createdAt'];
  const validKeyResult = (item) => hasExactKeys(item, keyResultKeys) &&
    isPositiveID(item.id) && typeof item.name === 'string' && typeof item.groupName === 'string' &&
    isConcurrency(item.currentConcurrency) && isCost(item.todayCost) && isCost(item.total30dCost) &&
    isCost(item.quota) && isCost(item.quotaUsed) && typeof item.lastUsedAt === 'string' &&
    typeof item.expiresAt === 'string' && typeof item.status === 'string' && isTimestamp(item.createdAt);
  const validSearchPayload = (payload) => hasExactKeys(payload, ['targetType', 'results', 'page', 'pageSize', 'total']) &&
    payload.targetType === 'key' && Array.isArray(payload.results) && payload.results.every(validKeyResult) &&
    typeof payload.page === 'number' && Number.isInteger(payload.page) && payload.page >= 1 &&
    typeof payload.pageSize === 'number' && Number.isInteger(payload.pageSize) && payload.pageSize === 20 &&
    typeof payload.total === 'number' && Number.isInteger(payload.total) && payload.total >= 0;

  const formatCost = (value) => {
    if (!value || value === '0') return '0.00';
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
  const addCell = (row, label, value, kind = '') => {
    const cell = document.createElement('td');
    cell.dataset.label = label;
    cell.textContent = value;
    cell.className = 'border-b border-slate-200 p-3 align-top dark:border-slate-800' +
      (kind === 'numeric' ? ' whitespace-nowrap tabular-nums' : kind === 'breakable' ? ' max-w-[240px] break-words' : '');
    row.appendChild(cell);
  };

  const statusBadgeClass = (status) => status === 'active'
    ? 'inline-flex items-center rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-semibold text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    : 'inline-flex items-center rounded-full bg-slate-100 px-2.5 py-0.5 text-xs font-semibold text-slate-600 dark:bg-slate-800 dark:text-slate-300';

  const addStatusCell = (row, label, status) => {
    const cell = document.createElement('td');
    cell.dataset.label = label;
    cell.className = 'border-b border-slate-200 p-3 align-top dark:border-slate-800';
    const badge = document.createElement('span');
    badge.className = statusBadgeClass(status);
    badge.textContent = status;
    cell.appendChild(badge);
    row.appendChild(cell);
  };

  const validDailyPayload = (payload) => hasExactKeys(payload, ['items', 'days']) &&
    typeof payload.days === 'number' && Array.isArray(payload.items) &&
    payload.items.every((item) => hasExactKeys(item, ['date', 'actualCost']) &&
      typeof item.date === 'string' && isCost(item.actualCost));

  const chartPalette = () => document.documentElement.classList.contains('dark')
    ? { grid: '#334155', axisText: '#94a3b8', line: '#2dd4bf', point: '#2dd4bf', tooltipBg: '#0f172a', tooltipDate: '#5eead4', tooltipCost: '#fff', hoverPoint: '#f87171' }
    : { grid: '#e9eaeb', axisText: '#52606d', line: '#0f766e', point: '#0f766e', tooltipBg: '#17202a', tooltipDate: '#e7f5f2', tooltipCost: '#fff', hoverPoint: '#b42318' };

  const renderLineChart = (items) => {
    chartWrap.textContent = '';
    if (items.length === 0) return;
    const palette = chartPalette();
    const width = 760;
    const height = 240;
    const padL = 56;
    const padR = 16;
    const padT = 16;
    const padB = 32;
    const plotW = width - padL - padR;
    const plotH = height - padT - padB;
    const maxCost = Math.max(...items.map((item) => +item.actualCost || 0), 0.001);
    const x = (i) => padL + (items.length === 1 ? plotW / 2 : (i / (items.length - 1)) * plotW);
    const y = (value) => padT + plotH - (Math.min(value, maxCost) / maxCost) * plotH;

    const svgNs = 'http' + '://www.w3.org/2000/svg';
    const svg = document.createElementNS(svgNs, 'svg');
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', '每日消耗折线图');

    // Grid lines + Y-axis labels.
    const ySteps = 4;
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
      label.setAttribute('x', String(padL - 8));
      label.setAttribute('y', String(yy + 4));
      label.setAttribute('text-anchor', 'end');
      label.setAttribute('font-size', '11');
      label.setAttribute('fill', palette.axisText);
      label.textContent = value.toFixed(2);
      svg.appendChild(label);
    }

    // X-axis labels (first, middle, last dates).
    const labelIndexes = [0, Math.floor((items.length - 1) / 2), items.length - 1];
    labelIndexes.forEach((i) => {
      const text = document.createElementNS(svgNs, 'text');
      text.setAttribute('x', String(x(i)));
      text.setAttribute('y', String(height - padB + 18));
      text.setAttribute('text-anchor', 'middle');
      text.setAttribute('font-size', '11');
      text.setAttribute('fill', palette.axisText);
      text.textContent = items[i].date;
      svg.appendChild(text);
    });

    // Line path.
    const points = items.map((item, i) => `${x(i)},${y(+item.actualCost || 0)}`);
    const polyline = document.createElementNS(svgNs, 'polyline');
    polyline.setAttribute('points', points.join(' '));
    polyline.setAttribute('fill', 'none');
    polyline.setAttribute('stroke', palette.line);
    polyline.setAttribute('stroke-width', '2');
    polyline.setAttribute('stroke-linejoin', 'round');
    polyline.setAttribute('stroke-linecap', 'round');
    svg.appendChild(polyline);

    // Data point circles (enlarged transparent hit area for hover).
    const circles = [];
    items.forEach((item, i) => {
      const circle = document.createElementNS(svgNs, 'circle');
      circle.setAttribute('cx', String(x(i)));
      circle.setAttribute('cy', String(y(+item.actualCost || 0)));
      circle.setAttribute('r', '3');
      circle.setAttribute('fill', palette.point);
      svg.appendChild(circle);
      circles.push(circle);
      const hit = document.createElementNS(svgNs, 'circle');
      hit.setAttribute('cx', String(x(i)));
      hit.setAttribute('cy', String(y(+item.actualCost || 0)));
      hit.setAttribute('r', '10');
      hit.setAttribute('fill', 'transparent');
      svg.appendChild(hit);
    });

    // Hover tooltip showing the date (x-axis) and cost (y-axis).
    const tooltip = document.createElementNS(svgNs, 'g');
    tooltip.setAttribute('visibility', 'hidden');
    const tooltipBg = document.createElementNS(svgNs, 'rect');
    tooltipBg.setAttribute('rx', '4');
    tooltipBg.setAttribute('fill', palette.tooltipBg);
    const tooltipDate = document.createElementNS(svgNs, 'text');
    tooltipDate.setAttribute('font-size', '11');
    tooltipDate.setAttribute('fill', palette.tooltipDate);
    const tooltipCost = document.createElementNS(svgNs, 'text');
    tooltipCost.setAttribute('font-size', '12');
    tooltipCost.setAttribute('font-weight', '600');
    tooltipCost.setAttribute('fill', palette.tooltipCost);
    tooltip.appendChild(tooltipBg);
    tooltip.appendChild(tooltipDate);
    tooltip.appendChild(tooltipCost);
    svg.appendChild(tooltip);

    let activeIndex = -1;
    const showTooltip = (index) => {
      if (activeIndex === index) return;
      if (activeIndex >= 0) {
        circles[activeIndex].setAttribute('r', '3');
        circles[activeIndex].setAttribute('fill', palette.point);
      }
      activeIndex = index;
      const px = x(index);
      const py = y(+items[index].actualCost || 0);
      circles[index].setAttribute('r', '5');
      circles[index].setAttribute('fill', palette.hoverPoint);
      tooltipDate.textContent = items[index].date;
      tooltipCost.textContent = `$${(+items[index].actualCost || 0).toFixed(4)}`;
      const tw = 120;
      const th = 34;
      const tx = Math.min(Math.max(px - tw / 2, padL), width - padR - tw);
      const ty = py - th - 8 < padT ? py + 12 : py - th - 8;
      tooltipBg.setAttribute('x', String(tx));
      tooltipBg.setAttribute('y', String(ty));
      tooltipBg.setAttribute('width', String(tw));
      tooltipBg.setAttribute('height', String(th));
      tooltipDate.setAttribute('x', String(tx + tw / 2));
      tooltipDate.setAttribute('y', String(ty + 13));
      tooltipDate.setAttribute('text-anchor', 'middle');
      tooltipCost.setAttribute('x', String(tx + tw / 2));
      tooltipCost.setAttribute('y', String(ty + 27));
      tooltipCost.setAttribute('text-anchor', 'middle');
      tooltip.setAttribute('visibility', 'visible');
    };
    const hideTooltip = () => {
      if (activeIndex >= 0) {
        circles[activeIndex].setAttribute('r', '3');
        circles[activeIndex].setAttribute('fill', palette.point);
      }
      activeIndex = -1;
      tooltip.setAttribute('visibility', 'hidden');
    };

    // Find the nearest data point as the mouse moves over the plot area.
    svg.addEventListener('mousemove', (event) => {
      const rect = svg.getBoundingClientRect();
      const vx = ((event.clientX - rect.left) / rect.width) * width;
      const vy = ((event.clientY - rect.top) / rect.height) * height;
      if (vx < padL || vx > width - padR || vy < padT || vy > height - padB) {
        hideTooltip();
        return;
      }
      let nearest = 0;
      let best = Infinity;
      items.forEach((_, i) => {
        const distance = Math.abs(x(i) - vx);
        if (distance < best) { best = distance; nearest = i; }
      });
      showTooltip(nearest);
    });
    svg.addEventListener('mouseleave', hideTooltip);

    chartWrap.appendChild(svg);
  };

  const renderDailyTable = (items) => {
    while (dailyBody.firstChild) dailyBody.removeChild(dailyBody.firstChild);
    items.forEach((item) => {
      const row = document.createElement('tr');
      addCell(row, '日期', item.date, 'breakable');
      addCell(row, '消耗费用 (USD)', `$${formatCost(item.actualCost)}`, 'numeric');
      dailyBody.appendChild(row);
    });
  };

  const setDialogStatus = (kind, message) => {
    dialogStatus.className = 'dialog-status min-h-[2rem] px-5 pb-4 text-sm text-slate-600 dark:text-slate-400';
    if (kind === 'error') dialogStatus.className += ' border-l-4 border-red-600 bg-red-50 text-red-700 dark:border-red-500 dark:bg-red-950/40 dark:text-red-400';
    dialogStatus.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    dialogStatus.textContent = message;
  };

  const openUsageDialog = async (keyId, keyName) => {
    currentKeyId = keyId;
    dialogTitle.textContent = `每日用量 - ${keyName}`;
    chartWrap.textContent = '';
    renderDailyTable([]);
    setDialogStatus('loading', '加载中...');
    usageDialog.showModal();
    await loadDailyUsage();
  };

  const loadDailyUsage = async () => {
    const days = parseInt(dialogDays.value, 10) || 30;
    setDialogStatus('loading', '加载中...');
    try {
      const response = await fetch('/api/key-usage', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keyId: currentKeyId, days }),
      });
      if (!response.ok) throw new Error('daily usage failed');
      const payload = await response.json();
      if (!validDailyPayload(payload)) throw new Error('invalid daily usage response');
      if (payload.items.length === 0) {
        chartWrap.textContent = '';
        renderDailyTable([]);
        setDialogStatus('empty', '该时间范围内暂无用量记录');
        return;
      }
      renderLineChart(payload.items);
      renderDailyTable(payload.items);
      setDialogStatus('ready', `${payload.items.length} 天的记录`);
    } catch (_) {
      setDialogStatus('error', '无法加载每日用量，请重试');
    }
  };

  dialogClose.addEventListener('click', () => { usageDialog.close(); });
  usageDialog.addEventListener('click', (event) => {
    if (event.target === usageDialog) usageDialog.close();
  });
  dialogDays.addEventListener('change', loadDailyUsage);

  const renderResults = () => {
    const totalPages = Math.max(1, Math.ceil(state.total / state.pageSize));
    resultCount.textContent = `共 ${state.total} 个 Key，第 ${state.page} / ${totalPages} 页`;
    pageStatus.textContent = `第 ${state.page} / ${totalPages} 页`;
    previousPageButton.disabled = state.page <= 1;
    nextPageButton.disabled = state.page >= totalPages;
    updateSortControls();
    clearRows();
    state.results.forEach((item) => {
      const row = document.createElement('tr');
      addCell(row, '名称', item.name, 'breakable');
      addCell(row, '分组', item.groupName || '无分组', 'breakable');
      addCell(row, '当前并发', String(item.currentConcurrency), 'numeric');
      addCell(row, '今日用量', `$${formatCost(item.todayCost)}`, 'numeric');
      addCell(row, '近30天用量', `$${formatCost(item.total30dCost)}`, 'numeric');
      addCell(row, '额度已用 / 总额度', `${formatCost(item.quotaUsed)} / ${formatQuota(item.quota)}`, 'numeric');
      addCell(row, '上次使用时间', formatTimestamp(item.lastUsedAt), 'breakable');
      addCell(row, '过期时间', formatTimestamp(item.expiresAt), 'breakable');
      addStatusCell(row, '状态', item.status);
      addCell(row, '创建时间', formatTimestamp(item.createdAt), 'breakable');
      const actionCell = document.createElement('td');
      actionCell.dataset.label = '每日用量';
      actionCell.className = 'border-b border-slate-200 p-3 align-top dark:border-slate-800';
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'row-action inline-flex h-8 min-w-[76px] items-center justify-center rounded-md border border-teal-700 px-3 text-sm font-semibold text-teal-700 hover:bg-teal-700 hover:text-white dark:border-teal-600 dark:text-teal-400 dark:hover:bg-teal-600 dark:hover:text-white';
      button.textContent = '每日用量';
      button.addEventListener('click', () => openUsageDialog(item.id, item.name));
      actionCell.appendChild(button);
      row.appendChild(actionCell);
      keyBody.appendChild(row);
    });
    renderKeyCards();
    resultsRegion.hidden = false;
  };

  const addCardRow = (card, label, value) => {
    const row = document.createElement('div');
    row.className = 'grid grid-cols-[minmax(96px,40%)_1fr] gap-3 py-1 text-sm';
    const labelCell = document.createElement('div');
    labelCell.className = 'font-semibold text-slate-600 dark:text-slate-400';
    labelCell.textContent = label;
    const valueCell = document.createElement('div');
    valueCell.className = 'break-words';
    valueCell.textContent = value;
    row.appendChild(labelCell);
    row.appendChild(valueCell);
    card.appendChild(row);
  };

  const addCardStatusRow = (card, label, status) => {
    const row = document.createElement('div');
    row.className = 'grid grid-cols-[minmax(96px,40%)_1fr] gap-3 py-1 text-sm';
    const labelCell = document.createElement('div');
    labelCell.className = 'font-semibold text-slate-600 dark:text-slate-400';
    labelCell.textContent = label;
    const valueCell = document.createElement('div');
    const badge = document.createElement('span');
    badge.className = statusBadgeClass(status);
    badge.textContent = status;
    valueCell.appendChild(badge);
    row.appendChild(labelCell);
    row.appendChild(valueCell);
    card.appendChild(row);
  };

  const renderKeyCards = () => {
    while (keyCards.firstChild) keyCards.removeChild(keyCards.firstChild);
    state.results.forEach((item) => {
      const card = document.createElement('article');
      card.className = 'rounded-lg border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900';
      addCardRow(card, '名称', item.name);
      addCardRow(card, '分组', item.groupName || '无分组');
      addCardRow(card, '当前并发', String(item.currentConcurrency));
      addCardRow(card, '今日用量', `$${formatCost(item.todayCost)}`);
      addCardRow(card, '近30天用量', `$${formatCost(item.total30dCost)}`);
      addCardRow(card, '额度已用 / 总额度', `${formatCost(item.quotaUsed)} / ${formatQuota(item.quota)}`);
      addCardRow(card, '上次使用时间', formatTimestamp(item.lastUsedAt));
      addCardRow(card, '过期时间', formatTimestamp(item.expiresAt));
      addCardStatusRow(card, '状态', item.status);
      addCardRow(card, '创建时间', formatTimestamp(item.createdAt));
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'row-action mt-3 inline-flex h-8 w-full items-center justify-center rounded-md border border-teal-700 text-sm font-semibold text-teal-700 hover:bg-teal-700 hover:text-white dark:border-teal-600 dark:text-teal-400 dark:hover:bg-teal-600 dark:hover:text-white';
      button.textContent = '每日用量';
      button.addEventListener('click', () => openUsageDialog(item.id, item.name));
      card.appendChild(button);
      keyCards.appendChild(card);
    });
  };

  const updateSortControls = () => {
    const update = (button, header, sortBy) => {
      const active = state.sortBy === sortBy;
      const direction = active ? state.sortDirection : '';
      const directionText = direction === 'desc' ? '降序' : direction === 'asc' ? '升序' : '默认';
      button.setAttribute('aria-pressed', String(active));
      button.setAttribute('aria-label', `${button.dataset.label}，${directionText}`);
      button.querySelector('.sort-direction').textContent = directionText;
      header.setAttribute('aria-sort', direction === 'desc' ? 'descending' : direction === 'asc' ? 'ascending' : 'none');
    };
    update(todaySortButton, todaySortHeader, 'todayCost');
    update(total30dSortButton, total30dSortHeader, 'total30dCost');
  };

  const loadSearch = async () => {
    clearDisplayedResults();
    if (state.searchController) state.searchController.abort();
    state.searchController = new AbortController();
    const sequence = ++state.searchSequence;
    setSearchBusy(true);
    setStatus(searchStatus, 'loading', '正在搜索...');
    try {
      const response = await fetch('/api/search', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(Object.assign(
          { targetType: 'key', page: state.page, sortBy: state.sortBy, sortDirection: state.sortDirection },
          state.query ? { query: state.query } : {}
        )),
        signal: state.searchController.signal,
      });
      if (sequence !== state.searchSequence) return;
      if (!response.ok) throw new Error('search failed');
      const payload = await response.json();
      if (!validSearchPayload(payload)) throw new Error('invalid response');
      state.results = payload.results;
      state.page = payload.page;
      state.pageSize = payload.pageSize;
      state.total = payload.total;
      setStatus(searchStatus, state.total === 0 ? 'empty' : 'results',
        state.total === 0 ? '未找到匹配的 Key' : `找到 ${state.total} 个 Key`);
      renderResults();
    } catch (error) {
      if (sequence !== state.searchSequence || error.name === 'AbortError') return;
      setStatus(searchStatus, 'error', '搜索失败，请重试', '如果问题持续，请检查查看器服务');
      queryInput.focus();
    } finally {
      if (sequence === state.searchSequence) {
        setSearchBusy(false);
        state.searchController = null;
      }
    }
  };

  const changeSort = (sortBy) => {
    if (state.sortBy === sortBy) {
      state.sortDirection = state.sortDirection === 'desc' ? 'asc' : 'desc';
    } else {
      state.sortBy = sortBy;
      state.sortDirection = 'desc';
    }
    state.page = 1;
    loadSearch();
  };

  searchForm.addEventListener('submit', (event) => {
    event.preventDefault();
    const validation = validateSearch(queryInput.value);
    if (validation.error) {
      queryInput.setAttribute('aria-invalid', 'true');
      setStatus(searchStatus, 'error', validation.error);
      queryInput.focus();
      return;
    }
    queryInput.removeAttribute('aria-invalid');
    state.query = validation.value;
    state.page = 1;
    loadSearch();
  });
  todaySortButton.addEventListener('click', () => changeSort('todayCost'));
  total30dSortButton.addEventListener('click', () => changeSort('total30dCost'));
  previousPageButton.addEventListener('click', () => { state.page--; loadSearch(); });
  nextPageButton.addEventListener('click', () => { state.page++; loadSearch(); });

  updateSortControls();
  loadSearch();
})();
