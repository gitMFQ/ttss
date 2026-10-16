package tts

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Service provides both Edge Read Aloud and Microsoft Translator synthesis.
type Service struct {
	httpClient *http.Client
	translator *translatorClient
	cacheMu    sync.Mutex
	voiceCache map[string]voiceCacheEntry
}

type voiceCacheEntry struct {
	voices  []Voice
	expires time.Time
}

func NewService() *Service {
	client := &http.Client{Timeout: 70 * time.Second}
	return &Service{
		httpClient: client,
		translator: &translatorClient{httpClient: client},
		voiceCache: make(map[string]voiceCacheEntry),
	}
}

func NormalizeService(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "edge":
		return "edge", nil
	case "ms", "mstranslator", "microsoft":
		return "ms", nil
	default:
		return "", fmt.Errorf("unsupported service; use edge or ms")
	}
}

func (s *Service) ListVoices(ctx context.Context, service string) ([]Voice, error) {
	canonical, err := NormalizeService(service)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s.cacheMu.Lock()
	if cached, ok := s.voiceCache[canonical]; ok && cached.expires.After(now) {
		voices := append([]Voice(nil), cached.voices...)
		s.cacheMu.Unlock()
		return voices, nil
	}
	// Serialize refreshes so simultaneous first requests share one upstream call.
	var voices []Voice
	if canonical == "edge" {
		voices, err = s.fetchEdgeVoices(ctx)
	} else {
		voices, err = s.translator.fetchVoices(ctx)
	}
	if err == nil {
		s.voiceCache[canonical] = voiceCacheEntry{voices: voices, expires: time.Now().Add(time.Hour)}
	}
	s.cacheMu.Unlock()
	if err != nil {
		return nil, err
	}
	return append([]Voice(nil), voices...), nil
}

func (s *Service) Convert(ctx context.Context, service, text string, options Options) ([]byte, string, error) {
	canonical, err := NormalizeService(service)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(text) == "" {
		return nil, "", fmt.Errorf("text is required")
	}
	if strings.TrimSpace(options.Voice) == "" {
		return nil, "", fmt.Errorf("voice is required")
	}
	if canonical == "edge" {
		speech, err := s.edgeSynthesize(ctx, text, options)
		return speech, "audio/mpeg", err
	}
	voices, err := s.ListVoices(ctx, "ms")
	if err != nil {
		return nil, "", err
	}
	voice := findVoice(voices, NormalizeVoiceName(options.Voice))
	audio, format, err := s.translator.synthesizeText(ctx, text, options, voice)
	if err != nil {
		return nil, "", err
	}
	return audio, OutputFormatContentType(format), nil
}

func findVoice(voices []Voice, value string) *Voice {
	for i := range voices {
		if strings.EqualFold(voices[i].Value, value) {
			return &voices[i]
		}
	}
	return nil
}

func OutputFormatContentType(format string) string {
	switch {
	case strings.HasPrefix(format, "webm-"):
		return "audio/webm"
	case strings.HasPrefix(format, "ogg-"):
		return "audio/ogg"
	case strings.HasPrefix(format, "riff-"):
		return "audio/wav"
	case strings.HasPrefix(format, "amr-"):
		return "audio/amr"
	default:
		return "audio/mpeg"
	}
}
