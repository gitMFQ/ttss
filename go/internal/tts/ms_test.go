package tts

import (
	"strings"
	"testing"
)

func TestNormalizeVoiceName(t *testing.T) {
	got := NormalizeVoiceName("Microsoft Server Speech Text to Speech Voice (zh-CN, XiaoxiaoNeural)")
	if got != "zh-CN-XiaoxiaoNeural" {
		t.Fatalf("unexpected short voice name: %q", got)
	}
}

func TestBuildMSSSMLEscapesAndOmni(t *testing.T) {
	ssml := buildMSSSML("A < B & C", "en-US-AvaNeural", "en-US", 0, 0, 100, "general", "default", 1, "", true)
	if !strings.Contains(ssml, "A &lt; B &amp; C") {
		t.Fatalf("text wasn't escaped: %s", ssml)
	}
	if strings.Contains(ssml, "express-as") || strings.Contains(ssml, "prosody") {
		t.Fatalf("Omni SSML should omit express-as/prosody: %s", ssml)
	}
}

func TestResolveEmotionBridge(t *testing.T) {
	result := resolveEmotionBridge("[[emo:开心]]你好呀", "", []string{"cheerful", "general"}, "", false)
	if result.text != "你好呀" || result.style != "cheerful" || !result.hasBridge {
		t.Fatalf("unexpected bridge result: %#v", result)
	}
}

func TestRandomUUIDStrings(t *testing.T) {
	value, err := randomUUIDString()
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '4' {
		t.Fatalf("invalid UUID v4: %q", value)
	}
}

func TestSafeOutputFormat(t *testing.T) {
	if got := stableOutputFormat("riff-24khz-16bit-mono-pcm", false, true); got != DefaultOutputFormat {
		t.Fatalf("safe mode format = %q", got)
	}
}
