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

// SelfLookupService returns the caller's own key detail and today's cost for
// an exact key-or-name credential match, plus the internal id (never
// returned to the client) so the handler can attach daily usage in the same
// request without a second round trip through the credential match.
type SelfLookupService interface {
	Lookup(ctx context.Context, credential string) (result search.SelfResult, id int64, ok bool, err error)
}

const selfLookupDailyUsageDays = 30

type selfLookupRequest struct {
	Credential string `json:"credential"`
}

func (application *handler) serveSelfLookup(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for self-lookup requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Use application/json for self-lookup requests.")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumSearchBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload selfLookupRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}

	if application.selfLookup == nil {
		writeError(response, http.StatusServiceUnavailable, "SELF_LOOKUP_UNAVAILABLE", "Self-lookup is temporarily unavailable.")
		return
	}

	result, id, ok, err := application.selfLookup.Lookup(request.Context(), payload.Credential)
	if err != nil {
		// Validation failures and infrastructure failures both surface as
		// this one generic response — the caller must not be able to tell
		// "your credential was malformed" apart from "nothing matched" or
		// "the database is down", since any of those distinctions could be
		// used to probe for valid keys.
		writeError(response, http.StatusServiceUnavailable, "SELF_LOOKUP_UNAVAILABLE", "Self-lookup is temporarily unavailable.")
		return
	}
	if !ok {
		writeError(response, http.StatusNotFound, "NOT_FOUND", "No key matched the provided credential.")
		return
	}

	result.DailyUsage = []search.DailyUsagePoint{}
	if application.dailyUsage != nil {
		if points, dailyErr := application.dailyUsage.DailyUsage(request.Context(), id, selfLookupDailyUsageDays); dailyErr == nil {
			result.DailyUsage = points
		}
		// A daily-usage failure here does not fail the whole lookup — the
		// caller still gets their key detail and today's cost; the chart
		// just renders as empty. The id used for this call is discarded
		// after this line and never reaches the response body.
	}
	writeJSON(response, http.StatusOK, result)
}
