package tts

import (
	"testing"
	"time"
)

// Reference vectors produced by edge-tts (Python) generate_sec_ms_gec_v2.
// They pin the ported algorithm bit-for-bit against upstream.
func TestGenerateSecMSGECv2(t *testing.T) {
	cases := []struct {
		unix  float64
		token string
	}{
		{1760844000.00, "434526ADA1E68E26-C92033D2240DBA7C3466B92AD2DA2E948EED9C269CA22273A21597B64878E33B"},
		{1760844137.31, "434526ADA1E68E26-C92033D2240DBA7C3466B92AD2DA2E948EED9C269CA22273A21597B64878E33B"},
		{1760844274.62, "434526ADA1E68E26-C92033D2240DBA7C3466B92AD2DA2E948EED9C269CA22273A21597B64878E33B"},
		{1760844411.93, "1AA792D17866BE30-AE99C60CD1ED9117054B60944BB4A355841FEC1DACE9EF26187602A8167FEA2F"},
		{1760844549.24, "1AA792D17866BE30-AE99C60CD1ED9117054B60944BB4A355841FEC1DACE9EF26187602A8167FEA2F"},
		{1760844686.55, "A419BFB2F5BDB116-26833230429FEBC0B2E2C0526BBB394D34E8238D5904A4959F07146AE138658F"},
		{1760844823.86, "A419BFB2F5BDB116-26833230429FEBC0B2E2C0526BBB394D34E8238D5904A4959F07146AE138658F"},
		{1760844961.17, "7BB23B2C00F6561E-7D9145932D2525516579191FBA10E79F3F935CDDE434BE0C1D113CE6B2A3CC47"},
		{1760845098.48, "7BB23B2C00F6561E-7D9145932D2525516579191FBA10E79F3F935CDDE434BE0C1D113CE6B2A3CC47"},
		{1760845235.79, "2C40AB1AED4AE5A9-3B479B2D58349CF4E912BD32FAD764FEA675B72041E57DE4024C6BC661E997E7"},
		{1760845373.10, "2C40AB1AED4AE5A9-3B479B2D58349CF4E912BD32FAD764FEA675B72041E57DE4024C6BC661E997E7"},
		{1760845510.41, "3AF6233723468FF2-E4830784DBC72E6C68E1239F50460B283F837F3A14661C4A7695E73C5F8B6EB1"},
	}

	for _, c := range cases {
		got, err := generateSecMSGECv2(c.unix)
		if err != nil {
			t.Fatalf("generateSecMSGECv2(%v) error: %v", c.unix, err)
		}
		if got != c.token {
			t.Errorf("generateSecMSGECv2(%v)\n got: %s\nwant: %s", c.unix, got, c.token)
		}
	}
}

// The token must change when the 300-second window rolls over and stay stable within it.
func TestGenerateSecMSGECv2Window(t *testing.T) {
	base := float64(1760844000)
	a, err := generateSecMSGECv2(base)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := generateSecMSGECv2(base + 299)
	if a != b {
		t.Errorf("token should be stable inside a 300s window: %s vs %s", a, b)
	}
	c, _ := generateSecMSGECv2(base + 300)
	if a == c {
		t.Errorf("token should change after the 300s window: %s", a)
	}
}

func TestGenerateSecMSGECv2Format(t *testing.T) {
	got, err := generateSecMSGECv2(float64(time.Now().Unix()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 81 {
		t.Errorf("expected 81 chars (16 hex + '-' + 64 hex), got %d: %s", len(got), got)
	}
	if got[16] != '-' {
		t.Errorf("expected '-' separator at index 16, got %q", got[16])
	}
}

func TestSecMSGECVersion(t *testing.T) {
	if secMSGECVersion == "" || secMSGECVersion[0] != '2' {
		t.Errorf("Sec-MS-GEC-Version must be v2, got %q", secMSGECVersion)
	}
}
