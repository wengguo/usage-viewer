package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

type LeaderboardService interface {
	Top(context.Context, int) (map[search.LeaderboardWindow][]search.LeaderboardEntry, error)
}

type leaderboardRequest struct {
	Limit int `json:"limit"`
}

type leaderboardResponse struct {
	Limit   int                                                    `json:"limit"`
	Windows map[search.LeaderboardWindow][]search.LeaderboardEntry `json:"windows"`
}

func (application *handler) serveLeaderboard(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for leaderboard requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Use application/json for leaderboard requests.")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumSearchBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload leaderboardRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}

	limit, err := search.ValidateLimit(payload.Limit)
	if err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_LIMIT", "Limit must be 10, 20, or 50.")
		return
	}
	if application.leaderboard == nil {
		writeError(response, http.StatusServiceUnavailable, "LEADERBOARD_UNAVAILABLE", "Leaderboard is temporarily unavailable.")
		return
	}

	windows, err := application.leaderboard.Top(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "LEADERBOARD_UNAVAILABLE", "Leaderboard is temporarily unavailable.")
		return
	}
	writeJSON(response, http.StatusOK, leaderboardResponse{Limit: limit, Windows: windows})
}
