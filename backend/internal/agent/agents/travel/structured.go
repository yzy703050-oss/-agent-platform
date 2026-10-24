package travel

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var auxiliaryKeyQuotes = regexp.MustCompile(`'([^']*)'(\s*:\s*)`)
var auxiliaryValueQuotes = regexp.MustCompile(`:\s*'([^']*)'`)
var auxiliaryTrailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// These recovery attempts are the original llm/structured.py helper behavior.
// Native tool arguments intentionally do not use this tolerant parser.
func parseAuxiliaryJSON(text string, out any) error {
	text = stripJSONFence(text)
	begin, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if begin < 0 || end <= begin {
		return errors.New("no JSON found in auxiliary model response")
	}
	raw := text[begin : end+1]
	if json.Valid([]byte(raw)) {
		return json.Unmarshal([]byte(raw), out)
	}
	cleaned := strings.Map(func(r rune) rune {
		if r <= 0x1f || r >= 0x7f && r <= 0x9f {
			return -1
		}
		return r
	}, raw)
	if json.Valid([]byte(cleaned)) {
		return json.Unmarshal([]byte(cleaned), out)
	}
	quoted := auxiliaryKeyQuotes.ReplaceAllString(raw, `"$1"$2`)
	quoted = auxiliaryValueQuotes.ReplaceAllString(quoted, `: "$1"`)
	if json.Valid([]byte(quoted)) {
		return json.Unmarshal([]byte(quoted), out)
	}
	if fixed := auxiliaryTrailingComma.ReplaceAllString(raw, `$1`); json.Valid([]byte(fixed)) {
		return json.Unmarshal([]byte(fixed), out)
	}
	var escaped strings.Builder
	inString, next := false, false
	for _, r := range raw {
		if next {
			escaped.WriteRune(r)
			next = false
			continue
		}
		if r == '\\' {
			escaped.WriteRune(r)
			next = true
			continue
		}
		if r == '"' {
			inString = !inString
		}
		if inString {
			switch r {
			case '\n':
				escaped.WriteString(`\n`)
				continue
			case '\r':
				escaped.WriteString(`\r`)
				continue
			case '\t':
				escaped.WriteString(`\t`)
				continue
			}
		}
		escaped.WriteRune(r)
	}
	if json.Valid([]byte(escaped.String())) {
		return json.Unmarshal([]byte(escaped.String()), out)
	}
	return errors.New("failed to parse auxiliary model JSON")
}
