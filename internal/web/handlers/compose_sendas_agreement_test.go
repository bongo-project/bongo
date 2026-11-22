package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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
	for _, sp := range testutil.SendAsProfiles {
		data, err := proto.Marshal(&bongov1.UserProfile{Email: sp.Email, Kind: sp.Kind, SharedMembers: sp.Members})
		if err != nil {
			t.Fatal(err)
		}
		if err := env.ps.Set([]byte("/user/"+sp.UserID+"/profile"), data); err != nil {
			t.Fatal(err)
		}
	}
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
				"send_id": {sendID()},
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
