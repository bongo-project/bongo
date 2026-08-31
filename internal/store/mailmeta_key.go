package store

import "strings"

// ParseMailMetaKey reports whether key is a direct /user/{id}/mail/{guid}
// entry -- a MailMeta record -- as opposed to some other key living under a
// user's tree. Both {id} and {guid} must be non-empty and contain no further
// "/", the same structural shape MailboxStore.OrphanedMailGUIDs uses to
// distinguish a MailMeta key from everything else under a user (mailbox.go).
//
// This replaces a substring test (bytes.Contains(key, []byte("/mail/"))).
// Mailbox message keys are /user/{id}/mailbox/{name}/msg/{uid}
// (store/mailbox.go); a mailbox literally named "mail" produces keys like
// /user/u1/mailbox/mail/msg/0000000001, which CONTAIN "/mail/" and so passed
// the substring test even though the value is a MailboxMessage, not a
// MailMeta.
//
// Such a value does NOT announce itself. proto.Unmarshal does not reject it:
// protobuf-go treats a wire-type mismatch on a known field number as an
// unknown field and skips it, so decoding a MailboxMessage as a MailMeta
// succeeds and yields a plausible-looking struct -- MailboxMessage.mail_guid
// (field 2, string) lands on MailMeta.message_id (field 2, string), and
// MailboxMeta.name lands on MailMeta.guid. (An earlier revision of this
// comment asserted that Unmarshal fails on these values. It does not; that was
// checked.) The guid this parser would return is wrong too:
// "msg/0000000001", not a GUID. Anything that then acts on the record -- a
// blob reference, a delete -- is acting on a misread key, which is exactly why
// the filter has to be structural rather than a substring.
//
// It lives in store rather than in agent because two whole-keyspace scans now
// need it -- the blob GC's reference collection (agent.collectReferencedBlobs)
// and the dangling-mail report (FindDanglingMail) -- and a second copy is how
// the two would later disagree about what counts as a MailMeta key.
func ParseMailMetaKey(key []byte) (userID, guid string, ok bool) {
	const prefix = "/user/"
	s := string(key)
	if !strings.HasPrefix(s, prefix) {
		return "", "", false
	}
	rest := s[len(prefix):]
	sep := strings.IndexByte(rest, '/')
	if sep <= 0 {
		return "", "", false
	}
	userID, rest = rest[:sep], rest[sep+1:]

	const mailSeg = "mail/"
	if !strings.HasPrefix(rest, mailSeg) {
		return "", "", false
	}
	guid = rest[len(mailSeg):]
	if guid == "" || strings.Contains(guid, "/") {
		return "", "", false
	}
	return userID, guid, true
}
