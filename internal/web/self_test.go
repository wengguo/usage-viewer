package web

import (
	"strings"
	"testing"
)

func TestSelfLookupPageIsCompleteAndSameOrigin(t *testing.T) {
	asset, err := Read("self.html")
	if err != nil {
		t.Fatalf("Read(self.html) error = %v", err)
	}
	if asset.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"自助查询", "/self.js", "/app.css",
		"self-form", "credential", "self-card", "self-chart-wrap",
		"self-quota-bar", "self-quota-percent", "self-name", "self-key",
		"排行榜", "Key 查询",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("self.html missing %q", required)
		}
	}
	for _, forbidden := range []string{"http://", "https://", "//cdn", "data:text/html"} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Errorf("self.html contains external or inline URL marker %q", forbidden)
		}
	}
}

func TestSelfLookupScriptIsSafeAndFetchesTheRightEndpoint(t *testing.T) {
	asset, err := Read("self.js")
	if err != nil {
		t.Fatalf("Read(self.js) error = %v", err)
	}
	if asset.ContentType != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"fetch('/api/self-lookup'", "keyMasked", "dailyUsage", "quotaUsed",
		"document.createElement", "appendChild", "status === 404",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("self.js missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"inner" + "HTML", "insertAdjacent" + "HTML", "local" + "Storage", "session" + "Storage",
		"document." + "cookie", "console.",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("self.js contains forbidden API %q", forbidden)
		}
	}
}
