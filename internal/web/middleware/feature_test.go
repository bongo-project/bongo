package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireFeature(t *testing.T) {
	tests := []struct {
		name     string
		state    LiveSessionState
		feature  string
		wantCode int
		wantRan  bool
	}{
		{"feature on", LiveSessionState{Found: true}, "rules", http.StatusOK, true},
		{"other feature off", LiveSessionState{Found: true, DisabledFeatures: []string{"vacation"}}, "rules", http.StatusOK, true},
		{"feature off", LiveSessionState{Found: true, DisabledFeatures: []string{"vacation", "rules"}}, "rules", http.StatusNotFound, false},
		{"state unavailable fails closed", LiveSessionState{}, "rules", http.StatusServiceUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			h := RequireAuth(testSecret, func(string) LiveSessionState { return tt.state })(
				RequireFeature(tt.feature)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ran = true
					w.WriteHeader(http.StatusOK)
				})))
			req := httptest.NewRequest("GET", "/settings/rules", nil)
			req.AddCookie(CreateSessionCookie(testSecret, "u1", "a@example.com", false, false, false, ""))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.wantCode || ran != tt.wantRan {
				t.Fatalf("code=%d ran=%v, want %d %v", rr.Code, ran, tt.wantCode, tt.wantRan)
			}
		})
	}
}

func TestFeatureDisabledWithoutAuth(t *testing.T) {
	if FeatureDisabled(httptest.NewRequest("GET", "/", nil).Context(), "rules") {
		t.Fatal("no session state must not report a feature disabled")
	}
}
