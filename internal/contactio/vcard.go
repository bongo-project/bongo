package contactio

import "strings"

// SplitVCards splits text into its BEGIN:VCARD ... END:VCARD blocks. Text
// outside a block is ignored. A block that never ends, or a BEGIN inside a
// block, is an incomplete card and an error naming its number; the blocks read
// so far are returned with it.
func SplitVCards(text []byte) ([][]byte, error) { return splitVCards(text, 0) }

func splitVCards(text []byte, max int) ([][]byte, error) {
	var (
		blocks [][]byte
		cur    []string
		inside bool
	)
	for rest := string(text); rest != ""; {
		var l string
		l, rest, _ = strings.Cut(rest, "\n")
		l = strings.TrimRight(l, "\r")
		t := strings.ToUpper(strings.TrimSpace(l))
		switch {
		case t == "BEGIN:VCARD":
			if inside {
				return blocks, &ParseError{Format: FormatVCard, N: len(blocks) + 1, Reason: ReasonIncomplete}
			}
			inside, cur = true, []string{l}
		case t == "END:VCARD" && inside:
			cur = append(cur, l)
			blocks = append(blocks, []byte(strings.Join(cur, "\n")+"\n"))
			if max > 0 && len(blocks) > max {
				return blocks, ErrTooMany
			}
			inside, cur = false, nil
		case inside:
			cur = append(cur, l)
		}
	}
	if inside {
		return blocks, &ParseError{Format: FormatVCard, N: len(blocks) + 1, Reason: ReasonIncomplete}
	}
	return blocks, nil
}

func parseVCard(text []byte, max int) (*Result, error) {
	blocks, err := splitVCards(text, max)
	if err != nil {
		return nil, err
	}
	res := &Result{Format: FormatVCard}
	for i, b := range blocks {
		res.Records = append(res.Records, Record{Ref: Ref{N: i + 1}, Raw: b})
	}
	return res, nil
}
