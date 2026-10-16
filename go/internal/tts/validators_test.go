package tts

import "testing"

func TestParseNumber(t *testing.T) {
	got, err := ParseNumber("rate", "-12", 0, -100, 100)
	if err != nil || got != -12 {
		t.Fatalf("got %v, err %v", got, err)
	}
	fractional, err := ParseNumber("rate", "1.25", 0, -100, 100)
	if err != nil || fractional != 1.25 {
		t.Fatalf("fractional value was not preserved: %v, err %v", fractional, err)
	}
	for _, value := range []string{"101", "NaN", "Inf", "-Inf"} {
		if _, err := ParseNumber("rate", value, 0, -100, 100); err == nil {
			t.Fatalf("expected invalid value error for %q", value)
		}
	}
}

func TestParseBool(t *testing.T) {
	got, err := ParseBool("safeMode", "false", true)
	if err != nil || got {
		t.Fatalf("got %v, err %v", got, err)
	}
}
