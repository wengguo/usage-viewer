package web

import (
	"embed"
	"errors"
)

//go:embed index.html app.css app.js credentials.html credentials.css credentials.js favicon.svg leaderboard.html leaderboard.js self.html self.js theme-init.js theme.js
var assets embed.FS

var ErrAssetNotFound = errors.New("web asset not found")

type Asset struct {
	Content     []byte
	ContentType string
}

func Read(name string) (Asset, error) {
	contentType := ""
	switch name {
	case "index.html", "credentials.html", "leaderboard.html", "self.html":
		contentType = "text/html; charset=utf-8"
	case "app.css", "credentials.css":
		contentType = "text/css; charset=utf-8"
	case "app.js", "credentials.js", "leaderboard.js", "self.js", "theme-init.js", "theme.js":
		contentType = "text/javascript; charset=utf-8"
	case "favicon.svg":
		contentType = "image/svg+xml; charset=utf-8"
	default:
		return Asset{}, ErrAssetNotFound
	}
	content, err := assets.ReadFile(name)
	if err != nil {
		return Asset{}, ErrAssetNotFound
	}
	return Asset{Content: content, ContentType: contentType}, nil
}
