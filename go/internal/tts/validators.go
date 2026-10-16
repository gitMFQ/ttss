package tts

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseNumber validates numeric TTS query parameters using the source API's ranges.
func ParseNumber(name, raw string, defaultValue, min, max float64) (float64, error) {
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < min || value > max {
		return 0, fmt.Errorf("invalid %s value", name)
	}
	return value, nil
}

func ParseBool(name, raw string, defaultValue bool) (bool, error) {
	if raw == "" {
		return defaultValue, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid %s value", name)
	}
}

func ValidOutputFormat(format string) bool { return contains(outputFormats, format) }
func ValidOutputRegion(region string) bool { return outputRegions[region] }
