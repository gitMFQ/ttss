package web

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ms-text-to-speech/internal/tts"
)

func TestAuthConfigurationAndLogin(t *testing.T) {
	handler := New(tts.NewService(), "secret", "").Handler()
	config := httptest.NewRecorder()
	handler.ServeHTTP(config, httptest.NewRequest("GET", "/api/config", nil))
	if config.Code != 200 || config.Body.String() == "" {
		t.Fatalf("config response: %d %s", config.Code, config.Body.String())
	}

	wrong := httptest.NewRequest("GET", "/api/text-to-speech?text=x&voice=y", nil)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, wrong)
	if unauthorized.Code != 401 {
		t.Fatalf("expected unauthorized, got %d", unauthorized.Code)
	}
}

func TestStaticUIAndLegadoTemplate(t *testing.T) {
	handler := New(tts.NewService(), "", "https://speech.example").Handler()
	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest("GET", "/", nil))
	if root.Code != 200 || !strings.Contains(root.Body.String(), "让文字") {
		t.Fatalf("root UI response: %d", root.Code)
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest("GET", "/app.css", nil))
	if asset.Code != 200 || !strings.Contains(asset.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("CSS asset response: %d %q", asset.Code, asset.Header().Get("Content-Type"))
	}

	request := httptest.NewRequest("GET", "/api/legado-import?voice=zh-CN-XiaoxiaoNeural&service=ms&protocol=https", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("Legado response: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("secret-capable Legado response should not be cached: %q", response.Header().Get("Cache-Control"))
	}
	var data struct {
		URL    string `json:"url"`
		Header string `json:"header"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data.URL, "rate={{(speakSpeed - 10) * 2}}&text={{java.encodeURI(speakText)}}") {
		t.Fatalf("Legado templates were encoded or lost: %s", data.URL)
	}
	if data.Header != "{}" {
		t.Fatalf("unexpected auth header: %q", data.Header)
	}
}

func TestPostValidationDoesNotCallProvider(t *testing.T) {
	handler := New(tts.NewService(), "", "").Handler()
	request := httptest.NewRequest("POST", "/api/text-to-speech", strings.NewReader(`{"text":"hello","service":"edge","options":{"voice":""}}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatalf("expected missing voice 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestParseOptionsAliasesAndDefaults(t *testing.T) {
	values := url.Values{"as": {"cheerful[hi]"}, "msSafeMode": {"false"}, "volume": {"0"}}
	options, err := parseOptions(values, "ms")
	if err != nil {
		t.Fatal(err)
	}
	if options.SafeMode == nil || *options.SafeMode {
		t.Fatalf("safeMode alias wasn't parsed: %#v", options.SafeMode)
	}
	if options.AutoStyleRules != "cheerful[hi]" || options.Volume != 0 {
		t.Fatalf("unexpected options: %#v", options)
	}
}
