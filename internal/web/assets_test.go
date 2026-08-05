package web

import (
	"errors"
	"strings"
	"testing"
)

func TestEmbeddedAssetsAreCompleteAndSameOrigin(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		required    []string
	}{
		{name: "index.html", contentType: "text/html; charset=utf-8", required: []string{"用量查询", "Key 名称或 Key", "请输入 Key 名称或 Key 值", "查找", "/app.css", "/app.js", "名称", "分组", "当前并发", "今日用量", "近30天用量", "额度已用 / 总额度", "上次使用时间", "过期时间", "状态", "创建时间"}},
		{name: "app.css", contentType: "text/css; charset=utf-8", required: []string{"#f7f8fa", "#0f766e", "@media (max-width: 639px)", "@media (prefers-reduced-motion: reduce)"}},
		{name: "app.js", contentType: "text/javascript; charset=utf-8", required: []string{"fetch('/api/search'", "targetType", "textContent", "AbortController", "formatCost", "formatTimestamp", "dataset.label", "正在搜索"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asset, err := Read(tt.name)
			if err != nil || asset.ContentType != tt.contentType {
				t.Fatalf("Read() content type=%q error=%v", asset.ContentType, err)
			}
			content := string(asset.Content)
			for _, required := range tt.required {
				if !strings.Contains(content, required) {
					t.Errorf("asset missing %q", required)
				}
			}
			for _, forbidden := range []string{"http://", "https://", "//cdn", "data:text/html"} {
				if strings.Contains(strings.ToLower(content), forbidden) {
					t.Errorf("asset contains external or inline URL marker %q", forbidden)
				}
			}
		})
	}
}

func TestBrowserScriptAvoidsPersistenceAndUnsafeHTMLSinks(t *testing.T) {
	asset, err := Read("app.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(asset.Content)
	for _, forbidden := range []string{
		"inner" + "HTML",
		"insertAdjacent" + "HTML",
		"local" + "Storage",
		"session" + "Storage",
		"document." + "cookie",
		"history." + "pushState",
		"history." + "replaceState",
		"console.",
		"service" + "Worker",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("browser script contains forbidden API %q", forbidden)
		}
	}
	for _, forbidden := range []string{
		"parse" + "Float",
		"Number(",
		"Intl." + "NumberFormat",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("browser script contains imprecise numeric API %q", forbidden)
		}
	}
	for _, required := range []string{"document.createElement", "appendChild", "removeChild", "state.searchSequence", "state.results"} {
		if !strings.Contains(content, required) {
			t.Errorf("browser script missing safe state/render primitive %q", required)
		}
	}
}

func TestKeySurfaceUsesExplicitApprovedFields(t *testing.T) {
	index, err := Read("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := Read("app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(index.Content) + string(script.Content)
	for _, required := range []string{
		"currentConcurrency", "todayCost", "total30dCost", "quota", "quotaUsed",
		"lastUsedAt", "expiresAt", "status", "createdAt", "groupName", "dataset.label",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("key surface missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"ledger" + "Id", "raw" + "Request", "raw" + "Response", "usage" + "_7d",
	} {
		if strings.Contains(strings.ToLower(combined), strings.ToLower(forbidden)) {
			t.Errorf("key surface contains excluded field %q", forbidden)
		}
	}
}

func TestReadRejectsEveryUnknownAsset(t *testing.T) {
	for _, name := range []string{"", "../index.html", "missing.js", "index.html/extra"} {
		if _, err := Read(name); !errors.Is(err, ErrAssetNotFound) {
			t.Errorf("Read(%q) error=%v", name, err)
		}
	}
}
