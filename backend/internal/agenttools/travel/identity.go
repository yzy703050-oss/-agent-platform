package travel

import (
	"bytes"
	"encoding/json"
	"io"
)

// Session JSON marshaling compacts RawMessage and escapes strings. Compare
// values without converting exact integers/decimals to float64. Identity,
// authorization and the original accepted arguments are checked separately.
func SameNativeArguments(a, b string) bool {
	canonical := func(text string) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewBufferString(text))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return json.Marshal(value)
	}
	left, err := canonical(a)
	if err != nil {
		return false
	}
	right, err := canonical(b)
	return err == nil && bytes.Equal(left, right)
}
