package tts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	translatorEndpoint = "https://dev.microsofttranslator.com/apps/endpoint?api-version=1.0"
	mtSignatureSecret  = "oik6PdDdMnOXemTbwvMn9de/h9lFnfBaCWbGMMZqqoSaQaqUOqjVGm5NqsmjcBI1x+sS9ugjB55HEJWRiFXYFw=="
)

var outputFormats = []string{
	"audio-16khz-32kbitrate-mono-mp3", "audio-16khz-64kbitrate-mono-mp3", "audio-16khz-128kbitrate-mono-mp3",
	"audio-24khz-48kbitrate-mono-mp3", "audio-24khz-96kbitrate-mono-mp3", DefaultOutputFormat,
	"audio-48khz-96kbitrate-mono-mp3", "audio-48khz-192kbitrate-mono-mp3",
	"webm-16khz-16bit-mono-opus", "webm-24khz-16bit-mono-opus", "webm-24khz-16bit-24kbps-mono-opus",
	"ogg-16khz-16bit-mono-opus", "ogg-24khz-16bit-mono-opus", "ogg-48khz-16bit-mono-opus", "amr-wb-16000hz",
	"riff-8khz-8bit-mono-alaw", "riff-8khz-8bit-mono-mulaw", "riff-8khz-16bit-mono-pcm",
	"riff-16khz-16bit-mono-pcm", "riff-22050hz-16bit-mono-pcm", "riff-24khz-16bit-mono-pcm",
	"riff-44100hz-16bit-mono-pcm", "riff-48khz-16bit-mono-pcm",
}

var outputRegions = map[string]bool{
	"auto": true, "hd": true, "eastasia": true, "southeastasia": true,
	"northeurope": true, "westeurope": true, "westus": true, "eastus": true,
}
var hdRegionMap = map[string]string{"eastasia": "southeastasia", "northeurope": "westeurope", "westus": "eastus"}
var omniLocales = map[string]bool{
	"zh-CN": true, "zh-HK": true, "zh-TW": true, "en-US": true, "en-GB": true, "ja-JP": true,
	"ko-KR": true, "fr-FR": true, "de-DE": true, "es-ES": true, "it-IT": true, "pt-BR": true, "ru-RU": true,
}

var voiceNamePattern = regexp.MustCompile(`\(([^,()]+),\s*([^()]+)\)`)
var localePattern = regexp.MustCompile(`^([a-z]{2,3}-[A-Za-z]{2,4}(?:-[A-Za-z]{2,4})?)`)

type translatorToken struct {
	region    string
	token     string
	expiresAt time.Time
}

type translatorClient struct {
	httpClient *http.Client
	mu         sync.Mutex
	cached     *translatorToken
}

type msVoiceInfo struct {
	Name                string   `json:"Name"`
	ShortName           string   `json:"ShortName"`
	Gender              string   `json:"Gender"`
	Locale              string   `json:"Locale"`
	LocalName           string   `json:"LocalName"`
	Status              string   `json:"Status"`
	VoiceType           string   `json:"VoiceType"`
	StyleList           []string `json:"StyleList"`
	RolePlayList        []string `json:"RolePlayList"`
	SecondaryLocaleList []string `json:"SecondaryLocaleList"`
	VoiceTag            struct {
		VoicePersonalities []string `json:"VoicePersonalities"`
	} `json:"VoiceTag"`
}

func (c *translatorClient) getToken(ctx context.Context, force bool) (translatorToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.cached != nil && c.cached.expiresAt.Add(-time.Minute).After(time.Now()) {
		return *c.cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, translatorEndpoint, nil)
	if err != nil {
		return translatorToken{}, err
	}
	req.Header.Set("Accept-Language", "zh-Hans")
	req.Header.Set("X-ClientVersion", "4.0.530a 5fe1dc6c")
	req.Header.Set("X-UserId", "0f04d16a175c411e")
	req.Header.Set("X-HomeGeographicRegion", "zh-Hans-CN")
	traceID, err := randomUUIDString()
	if err != nil {
		return translatorToken{}, err
	}
	req.Header.Set("X-ClientTraceId", traceID)
	signature, err := buildMSSignature(time.Now())
	if err != nil {
		return translatorToken{}, err
	}
	req.Header.Set("X-MT-Signature", signature)
	req.Header.Set("User-Agent", "okhttp/4.5.0")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return translatorToken{}, fmt.Errorf("get Microsoft Translator token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return translatorToken{}, upstreamError("get Microsoft Translator token", resp)
	}
	var data struct {
		Region string `json:"r"`
		Token  string `json:"t"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return translatorToken{}, fmt.Errorf("decode Microsoft token response: %w", err)
	}
	if data.Region == "" || data.Token == "" {
		return translatorToken{}, fmt.Errorf("Microsoft token response is missing region or token")
	}
	token := translatorToken{region: data.Region, token: data.Token, expiresAt: parseJWTExpiry(data.Token)}
	c.cached = &token
	return token, nil
}

func (c *translatorClient) fetchVoices(ctx context.Context) ([]Voice, error) {
	auth, err := c.getToken(ctx, false)
	if err != nil {
		return nil, err
	}
	request := func(auth translatorToken) (*http.Response, error) {
		endpoint := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/voices/list", auth.region)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", auth.token)
		return c.httpClient.Do(req)
	}
	resp, err := request(auth)
	if err != nil {
		return nil, fmt.Errorf("fetch Microsoft voices: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		auth, err = c.getToken(ctx, true)
		if err != nil {
			return nil, err
		}
		resp, err = request(auth)
		if err != nil {
			return nil, fmt.Errorf("retry Microsoft voices: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, upstreamError("fetch Microsoft voices", resp)
	}
	var entries []msVoiceInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode Microsoft voices: %w", err)
	}
	voices := make([]Voice, 0, len(entries))
	for _, entry := range entries {
		if entry.ShortName == "" || entry.Locale == "" || strings.Contains(strings.ToLower(entry.Status), "deprecated") || strings.Contains(strings.ToLower(entry.Status), "retired") {
			continue
		}
		personalities := entry.StyleList
		if len(personalities) == 0 {
			personalities = entry.VoiceTag.VoicePersonalities
		}
		voices = append(voices, Voice{
			Value: entry.ShortName, Label: firstNonempty(entry.LocalName, entry.ShortName), Locale: entry.Locale,
			Gender: entry.Gender, Format: DefaultOutputFormat, Personalities: personalities,
			Roles: entry.RolePlayList, SecondaryLocales: entry.SecondaryLocaleList,
			VoiceType: entry.VoiceType, OmniCapable: isOmniCapable(entry.ShortName, entry.VoiceType),
		})
	}
	return voices, nil
}

func (c *translatorClient) synthesizeText(ctx context.Context, text string, options Options, voice *Voice) ([]byte, string, error) {
	safeMode := options.IsSafeMode()
	voiceName := NormalizeVoiceName(options.Voice)
	omni := options.Omni && voice != nil && voice.OmniCapable
	finalVoice := voiceName
	if omni {
		finalVoice = strings.TrimSuffix(strings.TrimSuffix(voiceName, "Neural"), "neural") + ":DragonHDOmniLatestNeural"
	}
	format := options.Format
	if format == "" {
		format = DefaultOutputFormat
	}
	if !contains(outputFormats, format) {
		return nil, "", fmt.Errorf("unsupported output format")
	}
	format = stableOutputFormat(format, omni, safeMode)
	region := strings.ToLower(options.Region)
	if region == "" {
		region = "auto"
	}
	if !outputRegions[region] {
		return nil, "", fmt.Errorf("unsupported region")
	}
	langSkill := options.LangSkill
	if langSkill != "" {
		allowed := false
		if omni {
			allowed = omniLocales[langSkill]
		} else if voice != nil {
			allowed = contains(voice.SecondaryLocales, langSkill)
		}
		if !allowed {
			langSkill = ""
		}
	}
	styles := []string(nil)
	if voice != nil {
		styles = voice.Personalities
	}
	bridge := resolveEmotionBridge(text, options.Personality, styles, options.AutoStyleRules, safeMode)

	speed50 := 50 + options.Rate/2
	pitch50 := 50 + options.Pitch/2
	volume50 := 50 + options.Volume/2
	ratePct := math.Round(speed50 - 50)
	if safeMode {
		ratePct = clampFloat(ratePct, -10, 10)
	} else {
		ratePct = clampFloat(ratePct, -50, 50)
	}
	pitchPct := math.Round(pitch50 - 50)
	if safeMode {
		pitchPct = 0
	} else {
		pitchPct = clampFloat(pitchPct, -30, 30)
	}
	volume := math.Round(50 + volume50)
	if safeMode {
		volume = clampFloat(volume, 80, 120)
	} else {
		volume = clampFloat(volume, 40, 160)
	}
	role := "default"
	if !safeMode && options.Role != "" && !omni {
		role = options.Role
	}
	degree := options.StyleDegree
	if degree == 0 {
		degree = 1
	}
	if safeMode {
		degree = 1
	}
	if bridge.hasBridge && bridge.style != "general" {
		degree = 0.75
	}
	style := bridge.style
	if omni {
		style = ""
	}

	lang := "zh-CN"
	if match := localePattern.FindStringSubmatch(voiceName); len(match) > 0 {
		lang = match[1]
	}
	ssml := buildMSSSML(bridge.text, finalVoice, lang, int(ratePct), int(pitchPct), int(volume), style, role, degree, langSkill, omni)
	auth, err := c.getToken(ctx, false)
	if err != nil {
		return nil, "", err
	}
	request := func(auth translatorToken) (*http.Response, error) {
		serviceRegion := resolveServiceRegion(region, auth.region, omni)
		endpoint := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", serviceRegion)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(ssml))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", auth.token)
		req.Header.Set("Content-Type", "application/ssml+xml")
		req.Header.Set("X-Microsoft-OutputFormat", format)
		req.Header.Set("User-Agent", "okhttp/4.5.0")
		return c.httpClient.Do(req)
	}
	resp, err := request(auth)
	if err != nil {
		return nil, "", fmt.Errorf("synthesize with Microsoft TTS: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		auth, err = c.getToken(ctx, true)
		if err != nil {
			return nil, "", err
		}
		resp, err = request(auth)
		if err != nil {
			return nil, "", fmt.Errorf("retry Microsoft TTS: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", upstreamError("Microsoft TTS synthesis", resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("Microsoft TTS returned empty audio")
	}
	return body, format, nil
}

func NormalizeVoiceName(voice string) string {
	if strings.Contains(voice, "(") {
		if match := voiceNamePattern.FindStringSubmatch(voice); len(match) == 3 {
			return strings.TrimSpace(match[1]) + "-" + strings.TrimSpace(match[2])
		}
	}
	return voice
}

func isOmniCapable(shortName, voiceType string) bool {
	name := strings.ToLower(shortName)
	typeName := strings.ToLower(voiceType)
	return strings.HasSuffix(strings.ToLower(shortName), "neural") &&
		!strings.Contains(name, "multilingual") && !strings.Contains(name, "dragonhd") &&
		!strings.Contains(name, "hdflash") && !strings.Contains(name, "neuralhd") &&
		(typeName == "" || typeName == "neural")
}

func stableOutputFormat(format string, omni, safeMode bool) string {
	if safeMode {
		return DefaultOutputFormat
	}
	if omni && regexp.MustCompile(`(?i)48khz|opus|amr|8khz|22050`).MatchString(format) {
		return DefaultOutputFormat
	}
	return format
}

func resolveServiceRegion(setting, tokenRegion string, omni bool) string {
	requested := strings.ToLower(setting)
	if omni && requested == "auto" {
		requested = "hd"
	}
	if requested == "" || requested == "auto" {
		return tokenRegion
	}
	if requested == "hd" {
		if mapped, ok := hdRegionMap[tokenRegion]; ok {
			return mapped
		}
		return tokenRegion
	}
	if omni {
		if mapped, ok := hdRegionMap[requested]; ok {
			return mapped
		}
	}
	if outputRegions[requested] {
		return requested
	}
	return tokenRegion
}

func buildMSSSML(text, voice, lang string, rate, pitch, volume int, style, role string, degree float64, langSkill string, omni bool) string {
	content := escapeXML(text)
	if langSkill != "" {
		content = `<lang xml:lang="` + escapeXML(langSkill) + `">` + content + `</lang>`
	}
	if !omni {
		if style == "" {
			style = "general"
		}
		if role == "" {
			role = "default"
		}
		content = fmt.Sprintf(`<mstts:express-as style="%s" styledegree="%.2f" role="%s"><prosody rate="%d%%" pitch="%d%%" volume="%d%%">%s</prosody></mstts:express-as>`, escapeXML(style), math.Max(0, math.Min(2, degree)), escapeXML(role), rate, pitch, volume, content)
	}
	return `<speak xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="http://www.w3.org/2001/mstts" xmlns:emo="http://www.w3.org/2009/10/emotionml" version="1.0" xml:lang="` + escapeXML(lang) + `"><voice name="` + escapeXML(voice) + `">` + content + `</voice></speak>`
}

func buildMSSignature(now time.Time) (string, error) {
	path := strings.TrimPrefix(translatorEndpoint, "https://")
	encoded := url.QueryEscape(path)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	traceID, err := randomUUIDHex()
	if err != nil {
		return "", err
	}
	date := strings.ToLower(now.UTC().Format("Mon, 02 Jan 2006 15:04:05")) + "GMT"
	raw := strings.ToLower("MSTranslatorAndroidApp" + encoded + date + traceID)
	key, err := base64.StdEncoding.DecodeString(mtSignatureSecret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(raw))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return "MSTranslatorAndroidApp::" + signature + "::" + date + "::" + traceID, nil
}

func randomUUIDHex() (string, error) {
	b, err := randomUUIDBytes()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomUUIDString() (string, error) {
	value, err := randomUUIDHex()
	if err != nil {
		return "", err
	}
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:], nil
}

func randomUUIDBytes() ([]byte, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return b, nil
}

func parseJWTExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) >= 2 {
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil {
			var data struct {
				Exp int64 `json:"exp"`
			}
			if json.Unmarshal(payload, &data) == nil && data.Exp > 0 {
				return time.Unix(data.Exp, 0)
			}
		}
	}
	return time.Now().Add(5 * time.Minute)
}

func escapeXML(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "'", "&apos;")
	return value
}

func clampFloat(v, min, max float64) float64 { return math.Max(min, math.Min(max, v)) }
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func upstreamError(name string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	message := strings.TrimSpace(string(body))
	if len(message) > 300 {
		message = message[:300]
	}
	if message != "" {
		return fmt.Errorf("%s: HTTP %d: %s", name, response.StatusCode, message)
	}
	return fmt.Errorf("%s: HTTP %d", name, response.StatusCode)
}
