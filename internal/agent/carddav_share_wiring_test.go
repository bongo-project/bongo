package agent

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// An address book shared with a user must reach them over the real CardDAV
// endpoint, with the grant checked on every request and the privilege set
// rewritten for a read share. Deleting the SetGrants call or the
// PrivilegeFilter wrapper in agent.go turns a probe red while the per-package
// tests stay green.
func TestCardDAVSharedBookWiredIntoAgent(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)

	ownerID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	readerID, err := auth.CreateUser(a, a.Metadata, "reader@localhost", "Reader", "pass1234", false)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}
	writerID, err := auth.CreateUser(a, a.Metadata, "writer@localhost", "Writer", "pass1234", false)
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}
	const bookID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := a.AddressBooks.Create(ownerID, bookID, "Team", ""); err != nil {
		t.Fatal(err)
	}
	for id, level := range map[string]string{readerID: store.GrantLevelRead, writerID: store.GrantLevelWrite} {
		if err := a.ACL.SetGrant(store.ACLKindAddressBook, ownerID, bookID, store.GranteeUser(id), level, "", store.GrantStateAccepted, ownerID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Contacts.Create(ownerID, &bongov1.Contact{Guid: "shared-c", DisplayName: "Shared", Email: "s@example.com", AddressbookId: bookID}); err != nil {
		t.Fatal(err)
	}

	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	do := func(user, method, path, body string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.SetBasicAuth(user, "pass1234")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	seg := "shared-" + ownerID + "-" + bookID
	vcf := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Wired\r\nEMAIL:w@example.com\r\nEND:VCARD\r\n"
	ct := map[string]string{"Content-Type": "text/vcard"}

	rpath := "/carddav/" + readerID + "/addressbooks/" + seg + "/"
	if c, _ := do("reader@localhost", "GET", rpath+"shared-c.vcf", "", nil); c != http.StatusOK {
		t.Errorf("reader GET = %d, want 200", c)
	}
	if c, _ := do("reader@localhost", "PUT", rpath+"w.vcf", vcf, ct); c != http.StatusForbidden {
		t.Errorf("reader PUT = %d, want 403", c)
	}
	if c, _ := do("reader@localhost", "DELETE", rpath+"shared-c.vcf", "", nil); c != http.StatusForbidden {
		t.Errorf("reader DELETE = %d, want 403", c)
	}
	pf := `<?xml version="1.0"?><propfind xmlns="DAV:"><prop><current-user-privilege-set/></prop></propfind>`
	hd := map[string]string{"Depth": "0", "Content-Type": "application/xml"}
	c, body := do("reader@localhost", "PROPFIND", rpath, pf, hd)
	if c != http.StatusMultiStatus || !strings.Contains(body, "read") || strings.Contains(body, "write") {
		t.Errorf("reader privilege set: status %d body %s", c, body)
	}

	wpath := "/carddav/" + writerID + "/addressbooks/" + seg + "/"
	c, body = do("writer@localhost", "PROPFIND", wpath, pf, hd)
	if c != http.StatusMultiStatus || !strings.Contains(body, "write") {
		t.Errorf("writer privilege set: status %d body %s", c, body)
	}
	if c, _ := do("writer@localhost", "PUT", wpath+"w.vcf", vcf, ct); c != http.StatusCreated && c != http.StatusNoContent {
		t.Errorf("writer PUT = %d, want 201/204", c)
	}
	if ct, err := a.Contacts.Get(ownerID, "w"); err != nil || ct.AddressbookId != bookID {
		t.Errorf("writer's contact not stored under the owner: %+v, %v", ct, err)
	}

	if err := a.ACL.RevokeGrant(store.ACLKindAddressBook, ownerID, bookID, store.GranteeUser(readerID)); err != nil {
		t.Fatal(err)
	}
	if c, _ := do("reader@localhost", "GET", rpath+"shared-c.vcf", "", nil); c != http.StatusNotFound {
		t.Errorf("reader GET after revoke = %d, want 404", c)
	}
}
