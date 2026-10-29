import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join } from 'node:path';
import { chromium } from 'playwright-core';

const webRoot = join(process.cwd(), 'internal', 'web');
const contentTypes = {
  '.css': 'text/css; charset=utf-8',
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.svg': 'image/svg+xml; charset=utf-8',
};

const dateOffset = (offset) => {
  const date = new Date();
  date.setUTCDate(date.getUTCDate() + offset);
  return date.toISOString().slice(0, 10);
};

const dailyUsage = [
  { date: dateOffset(-2), actualCost: '1.25' },
  { date: dateOffset(-1), actualCost: '2.5' },
  { date: dateOffset(0), actualCost: '3.75' },
];

const json = (response, value) => {
  response.writeHead(200, { 'Content-Type': 'application/json' });
  response.end(`${JSON.stringify(value)}\n`);
};

const apiResponse = (request, response) => {
  if (request.url === '/api/auth/status') {
    json(response, { authenticated: true });
    return true;
  }
  if (request.url === '/api/self-lookup') {
    json(response, {
      keyMasked: 'sk-test...1234',
      name: 'Hover test key',
      groupName: 'Test group',
      quota: '100',
      quotaUsed: '7.5',
      status: 'active',
      expiresAt: '',
      todayCost: '3.75',
      dailyUsage,
    });
    return true;
  }
  if (request.url === '/api/search') {
    json(response, {
      targetType: 'key',
      results: [{
        id: 7,
        name: 'Hover test key',
        groupName: 'Test group',
        currentConcurrency: 0,
        todayCost: '3.75',
        total30dCost: '7.5',
        quota: '100',
        quotaUsed: '7.5',
        lastUsedAt: '',
        expiresAt: '',
        status: 'active',
        createdAt: '2026-08-12T00:00:00Z',
      }],
      page: 1,
      pageSize: 20,
      total: 1,
    });
    return true;
  }
  if (request.url === '/api/key-usage') {
    json(response, { items: dailyUsage, days: 30 });
    return true;
  }
  return false;
};

let browser;
let baseURL;
let server;

before(async () => {
  server = createServer(async (request, response) => {
    try {
      if (apiResponse(request, response)) return;
      const assetName = request.url === '/' ? 'self.html' : request.url === '/keys' ? 'index.html' : request.url.slice(1);
      const body = await readFile(join(webRoot, assetName));
      response.writeHead(200, { 'Content-Type': contentTypes[extname(assetName)] || 'application/octet-stream' });
      response.end(body);
    } catch (_) {
      response.writeHead(404);
      response.end('not found');
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  baseURL = `http://127.0.0.1:${address.port}`;
  browser = await chromium.launch({ channel: 'chrome', headless: true });
});

after(async () => {
  await browser?.close();
  await new Promise((resolve, reject) => server?.close((error) => error ? reject(error) : resolve()));
});

const assertChartHover = async (page, chartSelector, { padL, viewBoxWidth }) => {
  const svg = page.locator(`${chartSelector} svg`);
  await svg.waitFor();
  const box = await svg.boundingBox();
  assert.ok(box && box.width > 0 && box.height > 0, 'chart SVG must have a visible bounding box');

  await page.mouse.move(
    box.x + box.width / 2,
    box.y + box.height * 0.55,
  );

  const tooltip = svg.locator('[data-chart-tooltip="true"]');
  await tooltip.waitFor({ state: 'attached', timeout: 2000 });
  assert.equal(await tooltip.getAttribute('visibility'), 'visible');
  const tooltipText = await tooltip.textContent();
  assert.match(tooltipText, new RegExp(dailyUsage[1].date));
  assert.match(tooltipText, /\$2\.5000/);
  assert.equal(await svg.locator('circle[r="5"]').count(), 1);

  await page.mouse.move(
    box.x + (padL / viewBoxWidth) * box.width + 1,
    box.y + box.height * 0.55,
  );
  const tooltipX = Number(await tooltip.locator('rect').getAttribute('x'));
  assert.ok(tooltipX >= padL, `tooltip x ${tooltipX} must stay inside left plot boundary ${padL}`);

  await page.mouse.move(box.x + box.width + 10, box.y + box.height + 10);
  assert.equal(await tooltip.getAttribute('visibility'), 'hidden');
  assert.equal(await svg.locator('circle[r="5"]').count(), 0);
};

const captureHoveredChart = async (page, chartSelector, name, rerenderSelector) => {
  const screenshotDir = process.env.CHART_SCREENSHOT_DIR;
  if (!screenshotDir) return;
  const cases = [
    { suffix: 'desktop-light', width: 1280, height: 900, dark: false },
    { suffix: 'mobile-dark', width: 390, height: 844, dark: true },
  ];
  for (const item of cases) {
    await page.setViewportSize({ width: item.width, height: item.height });
    await page.evaluate(({ dark, rerenderSelector }) => {
      document.documentElement.classList.toggle('dark', dark);
      document.querySelector(rerenderSelector).dispatchEvent(new Event('change'));
    }, { dark: item.dark, rerenderSelector });
    const svg = page.locator(`${chartSelector} svg`);
    await svg.waitFor();
    await svg.scrollIntoViewIfNeeded();
    const box = await svg.boundingBox();
    assert.ok(box, 'chart SVG must remain visible for screenshots');
    await page.mouse.move(box.x + box.width / 2, box.y + box.height * 0.55);
    const tooltip = svg.locator('[data-chart-tooltip="true"]');
    assert.equal(await tooltip.getAttribute('visibility'), 'visible', JSON.stringify({ name, item, box }));
    await page.screenshot({ path: join(screenshotDir, `${name}-${item.suffix}.png`), fullPage: true });
  }
};

test('自助查询用量图悬停显示最近点的横纵坐标明细', async () => {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  try {
    await page.goto(`${baseURL}/`, { waitUntil: 'networkidle' });
    await page.locator('#credential').fill('hover-test-key');
    await page.locator('#self-form').evaluate((form) => form.requestSubmit());
    await page.locator('#self-card:not([hidden])').waitFor();
    await assertChartHover(page, '#self-chart-wrap', { padL: 48, viewBoxWidth: 640 });
    await captureHoveredChart(page, '#self-chart-wrap', 'self-chart', '#self-chart-days');
  } finally {
    await page.close();
  }
});

test('Key 用量图悬停显示最近点的横纵坐标明细', async () => {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  try {
    await page.goto(`${baseURL}/keys`, { waitUntil: 'networkidle' });
    await page.getByRole('button', { name: '每日用量' }).first().click();
    await page.locator('#usage-dialog[open]').waitFor();
    await assertChartHover(page, '#chart-wrap', { padL: 56, viewBoxWidth: 760 });
    await captureHoveredChart(page, '#chart-wrap', 'key-chart', '#dialog-days');
  } finally {
    await page.close();
  }
});
