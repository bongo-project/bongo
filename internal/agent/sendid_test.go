package agent

import (
	"strings"

	"github.com/google/uuid"
)

// composeSendID returns a fresh compose send ID (32 lowercase hex characters),
// as the compose form carries in its send_id field.
func composeSendID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
