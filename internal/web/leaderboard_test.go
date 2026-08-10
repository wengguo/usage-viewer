package web

import (
	"strings"
	"testing"
)

func TestLeaderboardPageIsCompleteAndSameOrigin(t *testing.T) {
	asset, err := Read("leaderboard.html")
	if err != nil {
		t.Fatalf("Read(leaderboard.html) error = %v", err)
	}
	if asset.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"消耗排行榜", "/leaderboard.js", "/app.css",
		"limit-select", "leaderboard-grid", "leaderboard-status",
		`data-window="1d"`, `data-window="3d"`, `data-window="7d"`, `data-window="30d"`,
		"排行榜", "自助查询", "Key 查询",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("leaderboard.html missing %q", required)
		}
	}
	for _, forbidden := range []string{"http://", "https://", "//cdn", "data:text/html"} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Errorf("leaderboard.html contains external or inline URL marker %q", forbidden)
		}
	}
}

func TestLeaderboardScriptIsSafeAndFetchesTheRightEndpoint(t *testing.T) {
	asset, err := Read("leaderboard.js")
	if err != nil {
		t.Fatalf("Read(leaderboard.js) error = %v", err)
	}
	if asset.ContentType != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"fetch('/api/leaderboard'", "keyMasked", "actualCost", "limit-select",
		"document.createElement", "appendChild",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("leaderboard.js missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"inner" + "HTML", "insertAdjacent" + "HTML", "local" + "Storage", "session" + "Storage",
		"document." + "cookie", "console.",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("leaderboard.js contains forbidden API %q", forbidden)
		}
	}
}
