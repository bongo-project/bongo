// Package testutil holds data shared by tests in different packages.
package testutil

// SendAsUserID and SendAsPrimary identify the acting user in SendAsCases.
const (
	SendAsUserID  = "u-alice"
	SendAsPrimary = "alice@example.com"
)

// SendAsRecord is an AddressRecord to seed before running SendAsCases.
type SendAsRecord struct {
	Addr     string
	UserID   string
	Kind     string
	CanSend  bool
	Verified bool
}

// SendAsRecords is the address data SendAsCases is evaluated against.
var SendAsRecords = []SendAsRecord{
	{"verified@gmail.com", SendAsUserID, "identity", true, true},
	{"unverified@gmail.com", SendAsUserID, "identity", true, false},
	{"alias@example.com", SendAsUserID, "alias", false, true},
	{"bobs@gmail.com", "u-bob", "identity", true, true},
	{"bob@example.com", "u-bob", "primary", true, true},
}

// SendAsCase is one row of the send-as agreement table. The same rows drive
// store.CanSendAs, web compose Send and SMTP submission so the three cannot
// drift apart.
type SendAsCase struct {
	Name    string
	Addr    string // single bare address; empty for header-only cases
	Headers string // header block for the SMTP DATA path
	Want    bool
}

// SendAsCases is acting as SendAsUserID whose primary is SendAsPrimary.
var SendAsCases = []SendAsCase{
	{"primary", "alice@example.com", "From: alice@example.com\r\n", true},
	{"primary upper", "ALICE@EXAMPLE.COM", "From: ALICE@EXAMPLE.COM\r\n", true},
	{"display name", "Alice@Example.com", "From: \"Name\" <Alice@Example.com>\r\n", true},
	{"verified external identity", "verified@gmail.com", "From: verified@gmail.com\r\n", true},
	{"other tenant", "ceo@tenantb.example", "From: ceo@tenantb.example\r\n", false},
	{"unverified identity", "unverified@gmail.com", "From: unverified@gmail.com\r\n", false},
	{"alias without can_send", "alias@example.com", "From: alias@example.com\r\n", false},
	{"another user's identity", "bobs@gmail.com", "From: bobs@gmail.com\r\n", false},
	{"another local user", "bob@example.com", "From: bob@example.com\r\n", false},
	{"unknown external", "ext@gmail.com", "From: ext@gmail.com\r\n", false},
	{"list with spoof last", "", "From: alice@example.com, ceo@tenantb.example\r\n", false},
	{"list with spoof first", "", "From: ceo@tenantb.example, alice@example.com\r\n", false},
	{"list all allowed", "", "From: alice@example.com, verified@gmail.com\r\n", true},
	{"two From headers", "", "From: alice@example.com\r\nFrom: ceo@tenantb.example\r\n", false},
	{"missing From", "", "To: x@y.example\r\n", false},
	{"unparseable From", "", "From: not an address\r\n", false},
	{"empty From", "", "From:\r\n", false},
	{"empty group From", "", "From: undisclosed:;\r\n", false},
}
