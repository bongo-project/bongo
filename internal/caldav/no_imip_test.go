package caldav

import (
	"go/build"
	"strings"
	"testing"
)

// CalDAV PUTs send no invitation mail: clients such as Thunderbird send iMIP
// themselves until the server advertises CalDAV scheduling, and a server copy
// would duplicate it. The backend has no way to send because it does not
// import the iMIP builder or the web handlers that deliver it; adding either
// import is the moment to decide on implicit scheduling (US-020) on purpose.
func TestBackendHasNoInviteSendPath(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.HasSuffix(imp, "/internal/imip") || strings.HasSuffix(imp, "/internal/web/handlers") {
			t.Errorf("internal/caldav imports %s: a CalDAV PUT would be able to send invitations", imp)
		}
	}
}
