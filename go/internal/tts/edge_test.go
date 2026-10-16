package tts

import (
	"strings"
	"testing"
	"time"
)

func TestEdgeFrameHelpers(t *testing.T) {
	headers := parseEdgeHeaders("X-RequestId:abc\r\nPath:turn.end\r\nContent-Type:application/json")
	if headers["x-requestid"] != "abc" || headers["path"] != "turn.end" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
	ssml := buildEdgeSSML("<hello & goodbye>", Options{Voice: `x"y`, Volume: 0})
	if !strings.Contains(ssml, "&lt;hello &amp; goodbye&gt;") || !strings.Contains(ssml, `name="x&quot;y"`) {
		t.Fatalf("unexpected SSML escaping: %s", ssml)
	}
}

func TestSecMSGECStableWithinFiveMinuteBucket(t *testing.T) {
	a := generateSecMSGEC(time.Unix(300, 0))
	b := generateSecMSGEC(time.Unix(301, 0))
	if a != b {
		t.Fatalf("token should be stable in same bucket: %q != %q", a, b)
	}
}
