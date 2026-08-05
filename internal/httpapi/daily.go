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

type DailyUsageService interface {
	DailyUsage(context.Context, int64, int) ([]search.DailyUsagePoint, error)
}

type dailyUsageRequest struct {
	KeyID int64 `json:"keyId"`
	Days  int   `json:"days"`
}

type dailyUsageResponse struct {
	Items []search.DailyUsagePoint `json:"items"`
	Days  int                      `json:"days"`
}

func (application *handler) serveDailyUsage(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for daily usage requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Use application/json for daily usage requests.")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumSearchBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload dailyUsageRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}
	if payload.KeyID <= 0 {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "A positive keyId is required.")
		return
	}
	days, err := search.ValidateDays(payload.Days)
	if err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_DAYS", "Days must be between 1 and 90.")
		return
	}
	if application.dailyUsage == nil {
		writeError(response, http.StatusServiceUnavailable, "DAILY_USAGE_UNAVAILABLE", "Daily usage is temporarily unavailable.")
		return
	}

	items, err := application.dailyUsage.DailyUsage(request.Context(), payload.KeyID, days)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "DAILY_USAGE_UNAVAILABLE", "Daily usage is temporarily unavailable.")
		return
	}
	writeJSON(response, http.StatusOK, dailyUsageResponse{Items: items, Days: days})
}
