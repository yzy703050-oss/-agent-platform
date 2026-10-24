package train

import (
	"regexp"
	"strings"
)

var sourceRailNumber = regexp.MustCompile(`^(?:[GDCZTKLSY]\d+|\d+)(?:/(?:[GDCZTKLSY]?\d+))?$`)

func sourceTrainKind(number string) string {
	number = strings.ToUpper(strings.TrimSpace(number))
	if !sourceRailNumber.MatchString(number) {
		return "unknown"
	}
	if strings.HasPrefix(number, "G") {
		return "high_speed"
	}
	if strings.HasPrefix(number, "D") {
		return "emu"
	}
	return "other"
}
