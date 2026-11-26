package tts

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Service provides both Edge Read Aloud and Microsoft Translator synthesis.
type Service struct {
	httpClient  *http.Client
	voiceClient *http.Client
	translator  *translatorClient

	poolMu       sync.Mutex
	edgePools    map[string]*edgePool
	preferV2Conv atomic.Bool
	preferV2Voic atomic.Bool
	closed       atomic.Bool

	// defaultEdgeEndpoint pins Edge synthesis to v1 or v2 when set (from the
	// EDGE_ENDPOINT env var); zero value keeps the automatic v2-first fallback.
	defaultEdgeEndpoint edgeEndpoint

	cfg        edgePoolConfig
	cacheMu    sync.Mutex
	voiceCache map[string]voiceCacheEntry
}

type voiceCacheEntry struct {
	voices  []Voice
	expires time.Time
}

// NewService constructs a service with a default HTTP client and the standard
// WebSocket dialer (which honours TTS_PROXY / HTTPS_PROXY / HTTP_PROXY).
// The EDGE_ENDPOINT environment variable (v1 or v2) may pin the default
// synthesis endpoint; an empty value keeps v2-first automatic fallback.
func NewService() *Service {
	return NewServiceWithDialer(newWebsocketDialer())
}

// SetDefaultEdgeEndpoint pins the default Edge endpoint (v1 or v2). Invalid or
// empty values are ignored, preserving the automatic fallback.
func (s *Service) SetDefaultEdgeEndpoint(value string) {
	if endpoint, ok := edgeEndpointByName(value); ok {
		s.defaultEdgeEndpoint = endpoint
	}
}

// NewServiceWithDialer lets callers inject a custom dialer (useful for tests).
func NewServiceWithDialer(dialer *websocketDialer) *Service {
	client := &http.Client{Timeout: 70 * time.Second}
	cfg := poolConfigFromEnv()
	s := &Service{
		httpClient:  client,
		voiceClient: newVoiceHTTPClient(cfg),
		translator:  &translatorClient{httpClient: client},
		cfg:         cfg,
		voiceCache:  make(map[string]voiceCacheEntry),
		edgePools:   make(map[string]*edgePool),
	}
	for _, endpoint := range edgeEndpoints {
		s.edgePools[endpoint.name] = newEdgePool(endpoint, dialer, cfg)
	}
	if endpoint, ok := edgeEndpointByName(os.Getenv("EDGE_ENDPOINT")); ok {
		s.defaultEdgeEndpoint = endpoint
		fmt.Printf("[EdgeTTS] default endpoint from EDGE_ENDPOINT=%s\n", endpoint.name)
	}
	// Default to v2 for synthesis, matching the Next.js implementation.
	s.preferV2Conv.Store(true)
	return s
}

// newVoiceHTTPClient builds the client used for the Edge voice list. The list
// is a plain HTTPS GET, so it must honour the same TTS_PROXY that the
// WebSocket path uses — otherwise users behind a proxy would fetch voices
// directly and only synthesis would be tunnelled.
func newVoiceHTTPClient(cfg edgePoolConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.proxy != nil {
		transport.Proxy = http.ProxyURL(cfg.proxyURL())
	}
	return &http.Client{Timeout: 70 * time.Second, Transport: transport}
}

func poolConfigFromEnv() edgePoolConfig {
	cfg := edgePoolConfig{
		poolSize:     3,
		idleTimeout:  5 * time.Minute,
		turnTimeout:  60 * time.Second,
		pingInterval: 30 * time.Second,
	}
	if v := strings.TrimSpace(os.Getenv("TTS_POOL_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 32 {
			cfg.poolSize = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("TTS_IDLE_TIMEOUT_MIN")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0.5 {
			cfg.idleTimeout = time.Duration(f * float64(time.Minute))
		}
	}
	if v := strings.TrimSpace(os.Getenv("TTS_TURN_TIMEOUT_SEC")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 5 {
			cfg.turnTimeout = time.Duration(f * float64(time.Second))
		}
	}
	if v := strings.TrimSpace(os.Getenv("TTS_PING_INTERVAL_SEC")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 5 {
			cfg.pingInterval = time.Duration(f * float64(time.Second))
		}
	}
	raw := pickProxyEnv()
	if raw != "" {
		if proxy, err := parseProxyURL(raw); err == nil {
			cfg.proxy = proxy
		}
	}
	return cfg
}

// pool returns the pool for one endpoint, or nil after Close.
func (s *Service) pool(endpoint edgeEndpoint) *edgePool {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	return s.edgePools[endpoint.name]
}

// edgeVoicesEndpoints orders the voice-list endpoints by the last healthy
// backend, without writing back the preference (it is recorded only when a
// fetch actually succeeds).
func (s *Service) edgeVoicesEndpoints() []edgeEndpoint {
	preferV2 := s.preferV2Voic.Load()
	if preferV2 {
		return edgeEndpoints
	}
	return []edgeEndpoint{edgeEndpoints[1], edgeEndpoints[0]}
}

// edgeConvertEndpoints orders the synthesis endpoints by the last healthy
// backend.
func (s *Service) edgeConvertEndpoints() []edgeEndpoint {
	preferV2 := s.preferV2Conv.Load()
	if preferV2 {
		return edgeEndpoints
	}
	return []edgeEndpoint{edgeEndpoints[1], edgeEndpoints[0]}
}

// noteEdgeBackend records which endpoint answered last for the given traffic
// kind (voices vs convert).
func (s *Service) noteEdgeBackend(endpoint edgeEndpoint, ok bool) {
	if ok {
		if endpoint.name == "v2" {
			s.preferV2Conv.Store(true)
		} else {
			s.preferV2Conv.Store(false)
		}
	}
}

func (s *Service) turnTimeout() time.Duration {
	return s.cfg.turnTimeout
}

// Close releases resources held by the service (notably the Edge TTS
// connection pool). Safe to call multiple times and concurrently with Convert.
func (s *Service) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.poolMu.Lock()
	pools := s.edgePools
	s.edgePools = nil
	s.poolMu.Unlock()
	for _, pool := range pools {
		pool.shutdown()
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
		if options.EdgeEndpoint != "" {
			if _, ok := edgeEndpointByName(options.EdgeEndpoint); !ok {
				return nil, "", fmt.Errorf("unsupported edge_endpoint; use v1 or v2")
			}
		}
		speech, err := s.edgeSynthesize(ctx, text, options)
		if err != nil {
			return nil, "", err
		}
		if len(speech) == 0 {
			// Microsoft answers "no speakable content" (pure punctuation or
			// symbols) with turn.end and no audio. Returning an empty body makes
			// clients such as Legado count it as an error, so substitute a short
			// legal silent MP3.
			if !hasSpeakableText(text) {
				return silentMP3Audio(), "audio/mpeg", nil
			}
			return nil, "", fmt.Errorf("Edge TTS returned empty audio")
		}
		return speech, "audio/mpeg", nil
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

// EdgePoolStats exposes pool state for diagnostics. The proxy URL is redacted
// so credentials embedded in it never leak through an API surface.
func (s *Service) EdgePoolStats() map[string]any {
	s.poolMu.Lock()
	pools := make(map[string]*edgePool, len(s.edgePools))
	for name, pool := range s.edgePools {
		pools[name] = pool
	}
	s.poolMu.Unlock()
	if len(pools) == 0 {
		return nil
	}
	perBackend := make(map[string]any, len(pools))
	for name, pool := range pools {
		st := pool.stats()
		perBackend[name] = map[string]any{
			"slots":  st.slots,
			"alive":  st.aliveSlots,
			"queued": st.totalQueued,
		}
	}
	return map[string]any{
		"backends": perBackend,
		"proxy":    redactProxy(pickProxyEnv()),
		"poolSize": s.cfg.poolSize,
	}
}

// redactProxy keeps the scheme/host/port of a proxy URL but drops credentials.
func redactProxy(raw string) string {
	if raw == "" {
		return ""
	}
	cfg, err := parseProxyURL(raw)
	if err != nil {
		return "<invalid>"
	}
	out := "http://" + cfg.host + ":" + cfg.port
	if cfg.username != "" {
		out = "http://***@" + cfg.host + ":" + cfg.port
	}
	return out
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
