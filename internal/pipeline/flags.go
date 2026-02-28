package pipeline

// Processing flags for QueueEnvelope.Flags (bitfield).
// These track which pipeline stages have been completed and their outcomes.
const (
	// MsgFlagSpamChecked indicates the anti-spam stage has run.
	MsgFlagSpamChecked uint32 = 1 << 0
	// MsgFlagVirusChecked indicates the anti-virus stage has run.
	MsgFlagVirusChecked uint32 = 1 << 1
	// MsgFlagRulesChecked indicates the filter rules stage has run.
	MsgFlagRulesChecked uint32 = 1 << 2
	// MsgFlagSpam indicates the message was classified as spam.
	MsgFlagSpam uint32 = 1 << 3
	// MsgFlagVirus indicates the message contained a virus.
	MsgFlagVirus uint32 = 1 << 4
	// MsgFlagQuarantined indicates the message has been quarantined.
	MsgFlagQuarantined uint32 = 1 << 5
	// MsgFlagDelivered indicates the message has been successfully delivered.
	// Used to prevent duplicate delivery on crash recovery.
	MsgFlagDelivered uint32 = 1 << 6
)

// HasFlag reports whether flags contains the given flag bit.
func HasFlag(flags, flag uint32) bool {
	return flags&flag != 0
}

// SetFlag returns flags with the given flag bit set.
func SetFlag(flags, flag uint32) uint32 {
	return flags | flag
}

// ClearFlag returns flags with the given flag bit cleared.
func ClearFlag(flags, flag uint32) uint32 {
	return flags &^ flag
}
