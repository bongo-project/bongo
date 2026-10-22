package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/testutil"
)

// TestComposeSendAs_AgreesWithSharedTable drives web compose through the same
// address table that store.CanSendAs and SMTP submission are tested against
// (internal/testutil.SendAsCases), so the three consumers of the one send-as
// rule cannot drift apart.
func TestComposeSendAs_AgreesWithSharedTable(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := testutil.SendAsUserID
	email := testutil.SendAsPrimary

	env.seedUser(t, userID, email)
	for _, r := range testutil.SendAsRecords {
		domain := r.Addr[strings.LastIndex(r.Addr, "@")+1:]
		env.seedAddress(t, r.Addr, r.UserID, domain, r.CanSend, r.Verified)
	}

	for _, tc := range testutil.SendAsCases {
		if tc.Addr == "" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			form := url.Values{
				"to":      {email},
				"subject": {"Agreement"},
				"body":    {"hello"},
				"from":    {tc.Addr},
			}
			req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withUserContext(req, userID, email)
			w := httptest.NewRecorder()
			env.handler.Send(w, req)

			want := http.StatusForbidden
			if tc.Want {
				want = http.StatusSeeOther
			}
			if w.Code != want {
				t.Fatalf("compose from %q: got %d, want %d (%s)", tc.Addr, w.Code, want, w.Body.String())
			}
		})
	}
}
