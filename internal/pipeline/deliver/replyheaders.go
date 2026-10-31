package deliver

import "strings"

// maxStoredReplyHeaderBytes bounds the Reply-To, List-Id and List-Post values
// kept on MailMeta, which every replica carries in its Raft log and Pebble.
const maxStoredReplyHeaderBytes = 998

// storedHeader prepares a reply or list header for MailMeta: trimmed, with
// CR and LF removed. A value over maxStoredReplyHeaderBytes is dropped rather
// than cut, because a truncated address list or URI could name a different
// recipient; readers fall back to the blob. The value is untrusted and is
// validated again where it is used.
func storedHeader(v string) string {
	v = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(v))
	if len(v) > maxStoredReplyHeaderBytes {
		return ""
	}
	return v
}
