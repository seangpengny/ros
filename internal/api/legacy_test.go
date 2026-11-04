package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyOnlineCount(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/v2/ros/online-count", nil)
	rec := httptest.NewRecorder()

	server.legacyOnlineCount(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if total, ok := res["total_online"].(float64); !ok || total < 1 {
		t.Fatalf("expected total_online >= 1, got %v", res["total_online"])
	}

	if _, ok := res["captured_at"].(string); !ok {
		t.Fatalf("expected captured_at string, got %v", res["captured_at"])
	}
}

