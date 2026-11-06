// Package runtimeprotocol validates transport values against the canonical
// OpenSpec schema. Validation here does not authorize calls or commit state.
package runtimeprotocol

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	v1 "agent-platform/backend/internal/model/runtimev1"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/gowebpki/jcs"
)

// This is a byte-for-byte deployment copy of the sole formal OpenSpec schema.
// Fixture tests compare it with that source so it cannot become another spec.
//
//go:embed agent-contracts.schema.json
var schemaBytes []byte

var schemaOnce sync.Once
var rootSchema jsonschema.Schema
var schemaErr error
var resolvedSchemas sync.Map

const wireLimit = 1_000_000

var errJSON = errors.New("invalid, ambiguous or lossy JSON")

func schemaFor(definition string) (*jsonschema.Resolved, *jsonschema.Schema, error) {
	schemaOnce.Do(func() { schemaErr = json.Unmarshal(schemaBytes, &rootSchema) })
	if schemaErr != nil {
		return nil, nil, errors.New("runtime schema unavailable")
	}
	schema, ok := rootSchema.Defs[definition]
	if !ok {
		return nil, nil, errors.New("unknown runtime definition")
	}
	if cached, ok := resolvedSchemas.Load(definition); ok {
		return cached.(*jsonschema.Resolved), schema, nil
	}
	resolved, err := (&jsonschema.Schema{Ref: "#/$defs/" + definition, Defs: rootSchema.Defs}).Resolve(nil)
	if err != nil {
		return nil, nil, errors.New("runtime schema resolution failed")
	}
	actual, _ := resolvedSchemas.LoadOrStore(definition, resolved)
	return actual.(*jsonschema.Resolved), schema, nil
}

// DecodeStrict validates first, then replaces the destination atomically with
// a fresh value. Missing fields cannot retain data from a previous request.
func DecodeStrict(raw []byte, definition string, dst any) error {
	value, err := safeValue(raw)
	if err != nil {
		return err
	}
	resolved, schema, err := schemaFor(definition)
	if err != nil {
		return err
	}
	if validateScalars(schema, value) != nil || resolved.Validate(schemaInstance(value)) != nil {
		return errors.New("runtime document violates schema")
	}
	ptr := reflect.ValueOf(dst)
	if ptr.Kind() != reflect.Pointer || ptr.IsNil() {
		return errors.New("destination must be a non-nil pointer")
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return errJSON
	}
	next := reflect.New(ptr.Elem().Type())
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(next.Interface()) != nil {
		return errors.New("runtime document does not match destination type")
	}
	ptr.Elem().Set(next.Elem())
	return nil
}

func InputHash(args json.RawMessage, meta v1.TrustedTravelMeta) (string, error) {
	var checked v1.TravelToolArgs
	if err := DecodeStrict(args, "TravelToolArgs", &checked); err != nil {
		return "", err
	}
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	var checkedMeta v1.TrustedTravelMeta
	if err = DecodeStrict(metaRaw, "TrustedTravelMeta", &checkedMeta); err != nil {
		return "", err
	}
	// Retain the original args bytes until canonicalization, including presence
	// and null. Credentials, parent epochs and transport IDs have no fields here.
	raw, err := json.Marshal(struct {
		SchemaVersion int                  `json:"schema_version"`
		Args          json.RawMessage      `json:"args"`
		Scope         v1.TrustedTravelMeta `json:"scope"`
	}{1, args, meta})
	if err != nil {
		return "", err
	}
	canonical, err := canonicalInput(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func ValidateResult(receipt v1.TravelTaskReceipt, result v1.TravelTaskResult) error {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	var r v1.TravelTaskReceipt
	if err = DecodeStrict(raw, "TravelTaskReceipt", &r); err != nil {
		return err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return err
	}
	var checked v1.TravelTaskResult
	if err = DecodeStrict(raw, "TravelTaskResult", &checked); err != nil {
		return err
	}
	if r.TaskID != checked.TaskID || r.RequestKey != checked.RequestKey || r.InputHash != checked.InputHash {
		return errors.New("result identity does not match receipt")
	}
	if r.Status != "queued" && r.Status != "running" && r.Status != checked.Status {
		return errors.New("terminal receipt cannot change status")
	}
	return nil
}

func canonicalInput(raw []byte) ([]byte, error) {
	if _, err := safeValue(raw); err != nil {
		return nil, err
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, errJSON
	}
	return canonical, nil
}

func safeValue(raw []byte) (any, error) {
	if len(raw) > wireLimit || !utf8.Valid(raw) || !validEscapedUnicode(raw) {
		return nil, errJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readValue(decoder, 0)
	if err != nil {
		return nil, errJSON
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errJSON
	}
	return value, nil
}

func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 10000 {
		return nil, errJSON
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok {
					return nil, errJSON
				}
				if _, ok = object[name]; ok {
					return nil, errJSON
				}
				child, err := readValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				object[name] = child
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errJSON
			}
			return object, nil
		case '[':
			array := []any{}
			for d.More() {
				child, err := readValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errJSON
			}
			return array, nil
		default:
			return nil, errJSON
		}
	case json.Number:
		f, err := strconv.ParseFloat(string(value), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errJSON
		}
		if f == 0 {
			mantissa, _, _ := strings.Cut(strings.ToLower(string(value)), "e")
			if strings.ContainsAny(mantissa, "123456789") {
				return nil, errJSON
			}
			return json.Number("0"), nil
		}
		rat, ok := new(big.Rat).SetString(string(value))
		if !ok {
			return nil, errJSON
		}
		if rat.IsInt() && rat.Num().BitLen() > 53 {
			return nil, errJSON
		}
		// Preserve the original decimal through schema validation. Converting
		// first would turn a fractional revision/amount into a valid integer.
		return value, nil
	default:
		return value, nil
	}
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Detect them
// before decoding so distinct caller inputs cannot collapse into one identity.
func validEscapedUnicode(raw []byte) bool {
	quoted := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		u, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if u >= 0xdc00 && u <= 0xdfff {
			return false
		}
		if u >= 0xd800 && u <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !quoted
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var timestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[Zz]|[+-](?:[01]\d|2[0-3]):[0-5]\d)$`)

// jsonschema-go treats format as annotation. Enforce the formal contract's
// date, date-time and URI formats explicitly, including nested union branches.
func validateScalars(s *jsonschema.Schema, value any) error {
	if s == nil || value == nil {
		return nil
	}
	if s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, "#/$defs/")
		if target, ok := rootSchema.Defs[name]; ok {
			if err := validateScalars(target, value); err != nil {
				return err
			}
		} else {
			return errJSON
		}
	}
	if number, ok := value.(json.Number); ok {
		exact, valid := new(big.Rat).SetString(string(number))
		if !valid {
			return errJSON
		}
		if s.Type == "integer" && !exact.IsInt() {
			return errJSON
		}
		if s.Const != nil {
			if constant, ok := (*s.Const).(float64); ok {
				if exact.Cmp(new(big.Rat).SetFloat64(constant)) != 0 {
					return errJSON
				}
			}
		}
	}
	if text, ok := value.(string); ok {
		switch s.Format {
		case "date":
			if !datePattern.MatchString(text) {
				return errJSON
			}
			if _, err := time.Parse("2006-01-02", text); err != nil {
				return errJSON
			}
		case "date-time":
			if !timestampPattern.MatchString(text) {
				return errJSON
			}
			if _, err := time.Parse(time.RFC3339Nano, strings.ToUpper(text)); err != nil {
				return errJSON
			}
		case "uri":
			u, err := url.Parse(text)
			if err != nil || !u.IsAbs() || strings.ContainsAny(text, " \t\r\n") {
				return errJSON
			}
		}
	}
	if object, ok := value.(map[string]any); ok {
		for name, child := range s.Properties {
			if err := validateScalars(child, object[name]); err != nil {
				return err
			}
		}
	}
	if array, ok := value.([]any); ok {
		for _, child := range array {
			if err := validateScalars(s.Items, child); err != nil {
				return err
			}
		}
	}
	for _, group := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf} {
		for _, child := range group {
			if err := validateScalars(child, value); err != nil {
				return err
			}
		}
	}
	for _, child := range []*jsonschema.Schema{s.Then, s.Else} {
		if err := validateScalars(child, value); err != nil {
			return err
		}
	}
	return nil
}

// The fixed schema library treats json.Number as a string for type checks.
// Preserve decimal values through our exact integer/const checks, then give the
// library the ordinary JSON values it supports for structural validation.
func schemaInstance(value any) any {
	switch v := value.(type) {
	case json.Number:
		f, _ := v.Float64()
		return f
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, child := range v {
			result[key] = schemaInstance(child)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, child := range v {
			result[i] = schemaInstance(child)
		}
		return result
	default:
		return value
	}
}
