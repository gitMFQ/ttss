package web

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	"ms-text-to-speech/internal/tts"
)

//go:embed static
var staticFiles embed.FS

type Server struct {
	speech    *tts.Service
	token     string
	publicURL string
}

func New(speech *tts.Service, token, publicURL string) *Server {
	return &Server{speech: speech, token: token, publicURL: strings.TrimRight(strings.TrimSpace(publicURL), "/")}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/voices", s.handleVoices)
	mux.HandleFunc("GET /api/text-to-speech", s.handleTTS)
	mux.HandleFunc("POST /api/text-to-speech", s.handleTTS)
	mux.HandleFunc("GET /api/legado-import", s.handleLegadoImport)
	mux.HandleFunc("GET /api/qrcode", s.handleQRCode)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /login", s.handleIndex)
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	return s.securityHeaders(s.accessLog(mux))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/login" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "UI unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"authRequired": s.token != "", "title": "MS Read Aloud Forwarder"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid login request"})
		return
	}
	if strings.TrimSpace(request.Token) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Token is required"})
		return
	}
	if s.token != "" && !secureEqual(s.token, request.Token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Token is invalid"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "token": request.Token})
}

func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	service, err := tts.NormalizeService(r.URL.Query().Get("service"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	voices, err := s.speech.ListVoices(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if locale := r.URL.Query().Get("locale"); locale != "" {
		filtered := voices[:0]
		for _, voice := range voices {
			if voice.Locale == locale {
				filtered = append(filtered, voice)
			}
		}
		voices = filtered
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, voices)
}

func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var text, serviceInput string
	var options tts.Options
	var err error
	if r.Method == http.MethodPost {
		var request struct {
			Text    string      `json:"text"`
			Service string      `json:"service"`
			Options tts.Options `json:"options"`
		}
		if decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); decodeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid TTS request"})
			return
		}
		text, serviceInput, options = request.Text, request.Service, request.Options
	} else {
		if len(r.URL.RawQuery) > 1<<20 {
			writeJSON(w, http.StatusRequestURITooLong, map[string]string{"error": "Request is too large"})
			return
		}
		query := r.URL.Query()
		text, serviceInput = query.Get("text"), query.Get("service")
		serviceInput, err = normalizedServiceOrDefault(serviceInput)
		if err == nil {
			options, err = parseOptions(query, serviceInput)
		}
		options.Voice = query.Get("voice")
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	service, err := tts.NormalizeService(serviceInput)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Text is required"})
		return
	}
	if len([]rune(text)) > 50000 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Text exceeds the 50000 character limit"})
		return
	}
	if strings.TrimSpace(options.Voice) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Voice is required"})
		return
	}
	if outsideRange(options.Rate, -100, 100) || outsideRange(options.Pitch, -100, 100) || outsideRange(options.Volume, -100, 100) || outsideRange(options.StyleDegree, 0, 2) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid TTS option range"})
		return
	}
	audio, contentType, err := s.speech.Convert(r.Context(), service, text, options)
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(strings.ToLower(err.Error()), "required") || strings.Contains(strings.ToLower(err.Error()), "unsupported") || strings.Contains(strings.ToLower(err.Error()), "invalid") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

func normalizedServiceOrDefault(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "edge", nil
	}
	return tts.NormalizeService(value)
}

func parseOptions(query url.Values, service string) (tts.Options, error) {
	volumeDefault := 100.0
	if service == "ms" {
		volumeDefault = 0
	}
	rate, err := tts.ParseNumber("rate", query.Get("rate"), 0, -100, 100)
	if err != nil {
		return tts.Options{}, err
	}
	pitch, err := tts.ParseNumber("pitch", query.Get("pitch"), 0, -100, 100)
	if err != nil {
		return tts.Options{}, err
	}
	volume, err := tts.ParseNumber("volume", query.Get("volume"), volumeDefault, -100, 100)
	if err != nil {
		return tts.Options{}, err
	}
	degree, err := tts.ParseNumber("styleDegree", query.Get("styleDegree"), 1, 0, 2)
	if err != nil {
		return tts.Options{}, err
	}
	safeRaw, exists := query["safeMode"]
	if !exists {
		safeRaw, exists = query["msSafeMode"]
	}
	safeMode := true
	if exists && len(safeRaw) > 0 {
		safeMode, err = tts.ParseBool("safeMode", safeRaw[0], true)
		if err != nil {
			return tts.Options{}, err
		}
	}
	omni, err := tts.ParseBool("omni", query.Get("omni"), false)
	if err != nil {
		return tts.Options{}, err
	}
	personality := query.Get("style")
	if personality == "" {
		personality = query.Get("personality")
	}
	autoRules := query.Get("autoStyleRules")
	if autoRules == "" {
		autoRules = query.Get("as")
	}
	return tts.Options{
		Volume: volume, Rate: rate, Pitch: pitch,
		Personality: personality, Role: query.Get("role"), StyleDegree: degree,
		Region: query.Get("region"), Format: query.Get("format"), LangSkill: query.Get("langSkill"),
		Omni: omni, SafeMode: &safeMode, AutoStyleRules: autoRules,
	}, nil
}

func outsideRange(value, min, max float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0) || value < min || value > max
}

func (s *Server) handleLegadoImport(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if s.token != "" && !secureEqual(s.token, query.Get("token")) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	voice := strings.TrimSpace(query.Get("voice"))
	if voice == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Voice is required"})
		return
	}
	service, err := tts.NormalizeService(query.Get("service"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	options, err := parseOptions(query, service)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if service == "ms" {
		format := options.Format
		if format == "" {
			format = tts.DefaultOutputFormat
		}
		if !tts.ValidOutputFormat(format) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unsupported output format"})
			return
		}
		region := options.Region
		if region == "" {
			region = "auto"
		}
		if !tts.ValidOutputRegion(strings.ToLower(region)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unsupported region"})
			return
		}
	}

	params := make(url.Values)
	params.Set("voice", voice)
	params.Set("volume", strconv.FormatFloat(options.Volume, 'f', -1, 64))
	params.Set("pitch", strconv.FormatFloat(options.Pitch, 'f', -1, 64))
	if options.Personality != "" {
		params.Set("personality", options.Personality)
	}
	if query.Get("style") != "" {
		params.Set("style", query.Get("style"))
	}
	if service == "ms" {
		params.Set("service", query.Get("service"))
		for _, key := range []string{"role", "styleDegree", "region", "format", "langSkill", "omni", "safeMode", "msSafeMode", "autoStyleRules", "as"} {
			if value, ok := query[key]; ok && len(value) > 0 {
				params.Set(key, value[0])
			}
		}
	} else if query.Has("service") {
		params.Set("service", query.Get("service"))
	}
	queryString := params.Encode()
	queryString += "&rate={{(speakSpeed - 10) * 2}}&text={{java.encodeURI(speakText)}}"
	if strings.HasPrefix(queryString, "&") {
		queryString = strings.TrimPrefix(queryString, "&")
	}
	baseURL, err := s.baseURL(r, query.Get("protocol"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	apiURL := baseURL + "/api/text-to-speech?" + queryString
	contentType := "audio/mpeg"
	if service == "ms" {
		format := options.Format
		if format == "" {
			format = tts.DefaultOutputFormat
		}
		if options.IsSafeMode() || (options.Omni && invalidOmniFormat(format)) {
			format = tts.DefaultOutputFormat
		}
		contentType = tts.OutputFormatContentType(format)
	}
	w.Header().Set("Cache-Control", "no-store")
	header := map[string]string{}
	if s.token != "" {
		header["Authorization"] = "Bearer " + s.token
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": voice, "contentType": contentType, "id": time.Now().UnixMilli(),
		"loginCheckJs": "", "loginUi": "", "loginUrl": "", "url": apiURL,
		"header": mustJSON(header),
	})
}

func invalidOmniFormat(format string) bool {
	lower := strings.ToLower(format)
	for _, fragment := range []string{"48khz", "opus", "amr", "8khz", "22050"} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func (s *Server) handleQRCode(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("url")
	if value == "" || len(value) > 4096 {
		http.Error(w, "Invalid QR URL", http.StatusBadRequest)
		return
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		http.Error(w, "Invalid QR URL", http.StatusBadRequest)
		return
	}
	png, err := qrcode.Encode(value, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "Unable to encode QR code", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) baseURL(r *http.Request, requestedProtocol string) (string, error) {
	if s.publicURL != "" {
		parsed, err := url.Parse(s.publicURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return "", fmt.Errorf("PUBLIC_BASE_URL must be an absolute http(s) URL")
		}
		return parsed.String(), nil
	}
	protocol := strings.ToLower(strings.TrimSpace(requestedProtocol))
	if protocol == "" {
		protocol = "http"
		if r.TLS != nil {
			protocol = "https"
		} else if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
			protocol = forwarded
		}
	}
	if protocol != "http" && protocol != "https" {
		return "", fmt.Errorf("protocol must be http or https")
	}
	if r.Host == "" {
		return "", fmt.Errorf("request host is missing")
	}
	return (&url.URL{Scheme: protocol, Host: r.Host}).String(), nil
}

func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	return secureEqual(s.token, strings.TrimPrefix(value, prefix))
}

func secureEqual(expected, provided string) bool {
	if len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func mustJSON(value any) string { data, _ := json.Marshal(value); return string(data) }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; media-src 'self' data: blob:; connect-src 'self'; font-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
		fmt.Printf("%s %s %s %s\n", time.Now().Format(time.RFC3339), remote, r.Method, r.URL.Path)
	})
}
