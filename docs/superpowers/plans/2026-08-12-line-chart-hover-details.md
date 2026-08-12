# 折线图悬停明细实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 让 Key 用量图和自助查询用量图都能在绘图区内吸附最近点，并显示该点的日期和四位小数金额。

**架构：** 保留 `app.js` 与 `self.js` 中各自的原生 SVG 渲染器，给两者统一稳定的提示层标识，并在自助查询图中补齐现有 Key 用量图的悬停状态、最近点选择和边界避让逻辑。使用 Playwright 驱动真实 Chromium，通过本地静态资源服务器和受控 API 响应测试最终 DOM 行为；Playwright 仅为开发依赖，不影响 Go 服务运行时。

**技术栈：** 原生 JavaScript、SVG DOM、Node.js 内置测试运行器、Playwright Core、Go 嵌入静态资源。

---

## 文件结构

- 创建 `package.json`：声明浏览器回归测试命令和 `playwright-core` 开发依赖。
- 创建 `package-lock.json`：锁定浏览器测试依赖版本。
- 创建 `tests/chart_hover_browser_test.mjs`：启动静态资源夹具服务器，用真实 Chromium 验证两张折线图的悬停行为。
- 修改 `internal/web/app.js`：给已经存在的提示组增加稳定、非视觉的测试选择器，并阻止提示层接收指针事件。
- 修改 `internal/web/self.js`：补充主题色、提示 SVG、点位高亮、最近点吸附、边界避让和隐藏恢复行为。

### 任务 1：建立两张折线图的浏览器行为回归测试

**文件：**
- 创建：`package.json`
- 创建：`package-lock.json`
- 创建：`tests/chart_hover_browser_test.mjs`
- 测试：`tests/chart_hover_browser_test.mjs`

测试要抓住的破坏是：任一页面没有在真实鼠标移动后显示最近点的日期/金额，或鼠标移出后没有隐藏提示并恢复点位。测试断言浏览器中的 SVG 可观察状态，不断言源码文本。

- [ ] **步骤 1：声明浏览器测试依赖和命令**

创建：

```json
{
  "name": "sub2api-usage-viewer-web-tests",
  "private": true,
  "scripts": {
    "test:charts": "node --test tests/chart_hover_browser_test.mjs"
  },
  "devDependencies": {
    "playwright-core": "1.61.1"
  }
}
```

运行 `npm install --ignore-scripts` 生成并锁定 `package-lock.json`；测试使用系统 Chrome，不下载浏览器二进制。

- [ ] **步骤 2：编写真实浏览器测试夹具**

`tests/chart_hover_browser_test.mjs` 使用 `node:http` 从 `internal/web` 提供静态文件，使用 `chromium.launch({ channel: 'chrome', headless: true })` 启动浏览器。为 `/api/self-lookup`、`/api/search`、`/api/key-usage` 返回完整、合法且手工定义的响应，例如：

```js
const dailyUsage = [
  { date: dateOffset(-2), actualCost: '1.25' },
  { date: dateOffset(-1), actualCost: '2.5' },
  { date: dateOffset(0), actualCost: '3.75' },
];
```

为每个页面封装同一行为断言：获取 `.chart-wrap svg`，把鼠标移动到绘图区中间的数据点附近，确认 `[data-chart-tooltip="true"]` 的 `visibility` 变成 `visible`、文本包含对应日期和 `$2.5000`、某个数据点半径变为 `5`；再把鼠标移到 SVG 外，确认提示隐藏且所有数据点半径恢复为 `3`。把鼠标移动到绘图区左边缘后，还要断言提示背景矩形的 `x` 不小于该图的左侧 padding，抓住边界避让回归。

自助查询测试填写 `#credential` 并提交 `#self-form`。Key 查询测试等待初始搜索结果，点击第一个“每日用量”按钮打开弹窗。两条测试均通过页面实际脚本、fetch、DOM 和鼠标事件完成。

- [ ] **步骤 3：运行测试验证红灯**

运行：

```bash
npm run test:charts
```

预期：FAIL。Key 用量图因缺少稳定的 `data-chart-tooltip` 标识失败，自助查询图因没有提示层和悬停行为失败。确认失败发生在提示层/可见性断言，而不是依赖、服务器或选择器初始化错误。

- [ ] **步骤 4：提交测试红灯基线**

```bash
git add package.json package-lock.json tests/chart_hover_browser_test.mjs
git commit -m "test: cover line chart hover details"
```

### 任务 2：统一 Key 用量图提示层契约

**文件：**
- 修改：`internal/web/app.js:238`
- 测试：`tests/chart_hover_browser_test.mjs`

- [ ] **步骤 1：给现有提示组增加稳定语义标识**

在创建 `tooltip` 后补充：

```js
tooltip.dataset.chartTooltip = 'true';
tooltip.setAttribute('pointer-events', 'none');
```

这不改变当前视觉行为，只让两张图共享稳定的提示层契约，并确保提示框不会成为鼠标事件目标。

- [ ] **步骤 2：运行 Key 用量图浏览器用例验证绿灯**

运行：

```bash
npm run test:charts -- --test-name-pattern="Key 用量图"
```

预期：PASS，提示显示日期和 `$2.5000`，移出后隐藏并恢复点位，左边缘提示框不越界。

- [ ] **步骤 3：提交 Key 图契约调整**

```bash
git add internal/web/app.js
git commit -m "test: expose key usage chart tooltip state"
```

### 任务 3：为自助查询折线图实现悬停明细

**文件：**
- 修改：`internal/web/self.js:115-194`
- 测试：`tests/chart_hover_browser_test.mjs`

- [ ] **步骤 1：扩展亮暗主题调色板**

让 `chartPalette` 与 Key 用量图包含相同的提示框颜色字段：

```js
const chartPalette = () => document.documentElement.classList.contains('dark')
  ? { grid: '#334155', axisText: '#94a3b8', line: '#2dd4bf', point: '#2dd4bf', tooltipBg: '#0f172a', tooltipDate: '#5eead4', tooltipCost: '#fff', hoverPoint: '#f87171' }
  : { grid: '#e9eaeb', axisText: '#52606d', line: '#0f766e', point: '#0f766e', tooltipBg: '#17202a', tooltipDate: '#e7f5f2', tooltipCost: '#fff', hoverPoint: '#b42318' };
```

- [ ] **步骤 2：保留点位引用并创建提示 SVG**

把点位循环改为收集 `circles`，在点位之后创建默认隐藏的 `g[data-chart-tooltip="true"]`，其内部依次包含背景 `rect`、日期 `text` 和金额 `text`。提示组设置 `pointer-events="none"`，文本字号与自助图当前 200px 高度匹配。

- [ ] **步骤 3：实现显示、隐藏和边界受限定位**

新增局部 `activeIndex`、`showTooltip(index)` 与 `hideTooltip()`。显示时恢复旧点、放大新点、设置日期与：

```js
tooltipCost.textContent = `$${(+items[index].actualCost || 0).toFixed(4)}`;
```

使用固定提示框尺寸，横坐标通过 `Math.min(Math.max(...))` 限制在 `padL` 和 `width - padR` 之间；上方不足时把提示框放到点位下方。隐藏时把活动点恢复为半径 `3` 和 `palette.point`。

- [ ] **步骤 4：实现绘图区最近点吸附事件**

在 SVG 上监听 `mousemove`，用 `getBoundingClientRect()` 把 `clientX/clientY` 换算为 viewBox 坐标。坐标位于绘图区之外时调用 `hideTooltip()`；在绘图区内遍历点位的 x 坐标，选择绝对横向距离最小者调用 `showTooltip(nearest)`。监听 `mouseleave` 调用 `hideTooltip`，最后保持现有 `chartWrap.appendChild(svg)`。

- [ ] **步骤 5：运行自助查询图浏览器用例验证绿灯**

运行：

```bash
npm run test:charts -- --test-name-pattern="自助查询用量图"
```

预期：PASS，提示显示最近点的日期和 `$2.5000`，移出后隐藏并恢复点位，左边缘提示框不越界。

- [ ] **步骤 6：运行完整浏览器回归测试**

运行：

```bash
npm run test:charts
```

预期：2 个测试全部 PASS。

- [ ] **步骤 7：提交自助查询图实现**

```bash
git add internal/web/self.js
git commit -m "feat: show self usage chart hover details"
```

### 任务 4：完整回归与视觉验收

**文件：**
- 验证：`internal/web/app.js`
- 验证：`internal/web/self.js`
- 验证：`tests/chart_hover_browser_test.mjs`

- [ ] **步骤 1：运行 JavaScript 语法和浏览器测试**

```bash
node --check internal/web/app.js
node --check internal/web/self.js
npm run test:charts
```

预期：两个语法检查退出码均为 0，两个浏览器测试全部 PASS，输出无未处理异常。

- [ ] **步骤 2：运行 Go 测试和构建**

```bash
go test ./...
go build ./...
```

预期：所有 Go 测试 PASS，构建退出码为 0。当前仓库不是 `saas-erp-backend`，因此不使用其专用 Redis 锁包装器；仍按仓库级 `AGENTS.md` 通过 `rtk` 执行命令。

- [ ] **步骤 3：检查变更质量和需求覆盖**

```bash
git diff --check
git status --short
```

逐项确认：两张图都支持绘图区最近点吸附；提示包含日期和四位小数金额；提示框左右不越界、上方不足时下移；移出隐藏并恢复点位；亮暗主题字段齐全；没有后端/API/运行时依赖变化。

- [ ] **步骤 4：在桌面和窄视口保存验收截图**

复用浏览器测试的静态服务器与受控响应，分别在 `1280x900` 和 `390x844` 视口、亮色和暗色主题下触发两张图悬停，将截图写到 `/tmp`。检查提示文字不被裁切、不遮挡相邻页面内容，点位高亮可见，图表本身没有布局位移。

- [ ] **步骤 5：提交验证中产生的必要修正**

若验证未产生代码修正则跳过；若产生修正，重新运行本任务全部验证后提交：

```bash
git add internal/web/app.js internal/web/self.js tests/chart_hover_browser_test.mjs package.json package-lock.json
git commit -m "fix: harden chart hover details"
```
