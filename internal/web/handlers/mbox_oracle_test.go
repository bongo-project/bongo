package handlers

import "strings"

// splitMbox is the original in-memory splitter, kept as the oracle that
// scanMbox must reproduce byte for byte.
func splitMbox(data []byte) [][]byte {
	var messages [][]byte
	content := string(data)

	if !strings.HasPrefix(content, "From ") {
		idx := strings.Index(content, "\nFrom ")
		if idx >= 0 {
			content = content[idx+1:]
		}
	}

	parts := strings.Split("\n"+content, "\nFrom ")
	for i, part := range parts {
		if i == 0 {
			continue
		}
		nlIdx := strings.Index(part, "\n")
		if nlIdx < 0 {
			continue
		}
		msgBody := part[nlIdx+1:]
		if len(strings.TrimSpace(msgBody)) == 0 {
			continue
		}
		messages = append(messages, []byte(msgBody))
	}
	return messages
}
