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
		{name: "index.html", contentType: "text/html; charset=utf-8", required: []string{"用量查询", "Key 名称或 Key", "请输入 Key 名称或 Key 值", "查找", "/app.css", "/app.js", "名称", "分组", "当前并发", "今日用量", "近30天用量", "上一页", "下一页", "sort-today-cost", "额度已用 / 总额度", "上次使用时间", "过期时间", "状态", "创建时间"}},
		{name: "app.css", contentType: "text/css; charset=utf-8", required: []string{".spinner", "@keyframes spin", "usage-dialog::backdrop", "prefers-reduced-motion"}},
		{name: "app.js", contentType: "text/javascript; charset=utf-8", required: []string{"fetch('/api/search'", "targetType", "textContent", "AbortController", "formatCost", "formatTimestamp", "dataset.label", "正在搜索", "pageSize", "sortDirection", "loadSearch();"}},
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
	for _, name := range []string{"", "../index.html", "missing.js", "index.html/extra", "vendor/tailwind.js"} {
		if _, err := Read(name); !errors.Is(err, ErrAssetNotFound) {
			t.Errorf("Read(%q) error=%v", name, err)
		}
	}
}

func TestThemeScriptsAreEmbeddedAsJavaScript(t *testing.T) {
	for _, name := range []string{"theme-init.js", "theme.js"} {
		asset, err := Read(name)
		if err != nil {
			t.Fatalf("Read(%s) error = %v", name, err)
		}
		if asset.ContentType != "text/javascript; charset=utf-8" {
			t.Fatalf("%s content type = %q", name, asset.ContentType)
		}
	}
}

// theme.js/theme-init.js are the one deliberate exception to the
// "no local persistence" rule elsewhere in this codebase: they may read and
// write exactly one localStorage key, 'theme', to remember the user's
// light/dark preference across the full-page navigations between these
// three static pages. Any other localStorage key, or any other forbidden
// browser API, must still be rejected the same as in the other scripts.
func TestThemeScriptsOnlyPersistTheThemePreference(t *testing.T) {
	for _, name := range []string{"theme-init.js", "theme.js"} {
		asset, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		content := string(asset.Content)
		for _, forbidden := range []string{
			"inner" + "HTML", "insertAdjacent" + "HTML", "session" + "Storage",
			"document." + "cookie", "console.", "history." + "pushState", "history." + "replaceState",
		} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s contains forbidden API %q", name, forbidden)
			}
		}
		calls := strings.Count(content, "local"+"Storage.getItem") + strings.Count(content, "local"+"Storage.setItem")
		totalCalls := strings.Count(content, "local"+"Storage.")
		if calls != totalCalls {
			t.Errorf("%s calls localStorage with something other than getItem/setItem", name)
		}
		if strings.Contains(content, "local"+"Storage") && !strings.Contains(content, "'theme'") {
			t.Errorf("%s touches localStorage without the 'theme' key", name)
		}
		for _, other := range []string{"'session'", "'auth'", "'token'", "'user'", "'credential'"} {
			if strings.Contains(content, other) {
				t.Errorf("%s references a non-theme storage key %q", name, other)
			}
		}
	}
}
