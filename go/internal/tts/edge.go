package tts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// osGetenv is a tiny indirection so tests can stub the environment lookup.
var osGetenv = os.Getenv

const (
	trustedClientToken = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	// chromiumVersion tracks the version upstream edge-tts ships. Claiming a major
	// newer than what Microsoft has deployed can trip fingerprint checks.
	chromiumVersion = "144.0.3719.82"
	chromiumMajor   = "144"
	// secMSGECVersion must carry the v2 prefix: the older v1 token format is now
	// rejected with HTTP 403 during the WebSocket handshake.
	secMSGECVersion = "2-" + chromiumVersion
	// edgeUserAgent presents as desktop Edge, matching upstream edge-tts headers.
	edgeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" +
		chromiumMajor + ".0.0.0 Safari/537.36 Edg/" + chromiumMajor + ".0.0.0"
)

// edgeEndpoint describes one Edge TTS backend. Both endpoints speak the same
// WebSocket protocol; only the host and the auth query parameter differ.
type edgeEndpoint struct {
	name string
	// voicesURL is the voice list endpoint including its auth query.
	voicesURL string
	// wsBaseURL is the WebSocket base (no query).
	wsBaseURL string
	// tokenParamName is the auth query key: v1 uses TrustedClientToken,
	// v2 uses Ocp-Apim-Subscription-Key.
	tokenParamName string
	// host is sent as the Host header and used for TLS/SNI and CONNECT.
	host string
}

var edgeEndpoints = []edgeEndpoint{
	{
		name:           "v2",
		voicesURL:      "https://api.msedgeservices.com/tts/cognitiveservices/voices/list?Ocp-Apim-Subscription-Key=" + trustedClientToken,
		wsBaseURL:      "wss://api.msedgeservices.com/tts/cognitiveservices/websocket/v1",
		tokenParamName: "Ocp-Apim-Subscription-Key",
		host:           "api.msedgeservices.com",
	},
	{
		name:           "v1",
		voicesURL:      "https://speech.platform.bing.com/consumer/speech/synthesize/readaloud/voices/list?trustedclienttoken=" + trustedClientToken,
		wsBaseURL:      "wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1",
		tokenParamName: "TrustedClientToken",
		host:           "speech.platform.bing.com",
	},
}

// attemptBudget limits one synthesis request across all retries. Retrying
// inside the slot (connection lost before answer) is cheaper and not counted
// here; only fresh backend attempts consume this budget.
const edgeAttemptBudget = 45 * time.Second

// retryBackoff pauses briefly between backend attempts.
const retryBackoff = 300 * time.Millisecond

type edgeVoice struct {
	Name           string `json:"Name"`
	FriendlyName   string `json:"FriendlyName"`
	Gender         string `json:"Gender"`
	Locale         string `json:"Locale"`
	SuggestedCodec string `json:"SuggestedCodec"`
	VoiceTag       struct {
		VoicePersonalities []string `json:"VoicePersonalities"`
	} `json:"VoiceTag"`
}

func (s *Service) fetchEdgeVoices(ctx context.Context) ([]Voice, error) {
	var lastErr error
	for _, endpoint := range s.edgeVoicesEndpoints() {
		voices, err := fetchEdgeVoicesFrom(ctx, s.voiceClient, endpoint)
		if err == nil {
			s.preferV2Voic.Store(endpoint.name == "v2")
			return voices, nil
		}
		lastErr = err
		fmt.Printf("[edge-tts] voices fail via %s: %v\n", endpoint.name, err)
	}
	return nil, lastErr
}

func fetchEdgeVoicesFrom(ctx context.Context, client *http.Client, endpoint edgeEndpoint) ([]Voice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.voicesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MS Read Aloud Forwarder)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Edge voices via %s: %w", endpoint.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, upstreamError("fetch Edge voices via "+endpoint.name, resp)
	}
	var entries []edgeVoice
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode Edge voices: %w", err)
	}
	voices := make([]Voice, 0, len(entries))
	for _, item := range entries {
		voices = append(voices, Voice{
			Value: item.Name, Label: item.FriendlyName, Locale: item.Locale, Gender: item.Gender,
			Format: item.SuggestedCodec, Personalities: item.VoiceTag.VoicePersonalities,
		})
	}
	return voices, nil
}

// edgeSynthesize submits a turn to the shared connection pool with v2-first
// backend aggregation: a failed attempt on one endpoint is retried on the
// other, and the last healthy backend is remembered for future requests.
func (s *Service) edgeSynthesize(ctx context.Context, text string, options Options) ([]byte, error) {
	startedAt := time.Now()
	var lastErr error
	for _, endpoint := range s.edgeConvertEndpoints() {
		if spent := time.Since(startedAt); spent >= edgeAttemptBudget {
			break
		}
		audio, err := s.edgeSynthesizeOnce(ctx, endpoint, text, options)
		if err == nil {
			s.noteEdgeBackend(endpoint, true)
			fmt.Printf("[edge-tts] synth ok via %s bytes=%d ms=%d\n", endpoint.name, len(audio), time.Since(startedAt).Milliseconds())
			return audio, nil
		}
		lastErr = err
		s.noteEdgeBackend(endpoint, false)
		fmt.Printf("[edge-tts] synth fail via %s after %dms: %v\n", endpoint.name, time.Since(startedAt).Milliseconds(), err)
		time.Sleep(retryBackoff)
	}
	return nil, fmt.Errorf("Edge TTS exhausted all backends: %w", lastErr)
}

// edgeSynthesizeOnce runs one turn through the endpoint's slot pool. The
// caller's context is capped here so queue wait + synthesis cannot pin an HTTP
// handler forever.
func (s *Service) edgeSynthesizeOnce(ctx context.Context, endpoint edgeEndpoint, text string, options Options) ([]byte, error) {
	pool := s.pool(endpoint)
	if pool == nil {
		return nil, errEdgeNoDialer
	}
	requestID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.turnTimeout())
	defer cancel()

	task := newEdgeTask(ctx, requestID, text, options)
	select {
	case r := <-pool.submit(task):
		return r.audio, r.err
	case <-ctx.Done():
		// The task is already cancelled through ctx; deliver is idempotent, so
		// whichever path wins is safe.
		task.deliver(edgeTaskResult{err: ctx.Err()})
		return nil, ctx.Err()
	}
}

func buildEdgeSSML(text string, options Options) string {
	return `<speak xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="http://www.w3.org/2001/mstts" xmlns:emo="http://www.w3.org/2009/10/emotionml" version="1.0" xml:lang="en-US"><voice name="` + escapeXML(options.Voice) + `"><prosody volume="` + fmt.Sprintf("%g%%", options.Volume) + `" rate="` + fmt.Sprintf("%g%%", options.Rate) + `" pitch="` + fmt.Sprintf("%g%%", options.Pitch) + `">` + escapeXML(text) + `</prosody></voice></speak>`
}

func randomHex(n int) (string, error) {
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

// newMuid returns an uppercase 32-hex MUID for the Edge handshake Cookie.
// The upstream client regenerates one per socket; failures fall back to a
// zero MUID rather than aborting the dial.
func newMuid() string {
	raw, err := randomHex(16)
	if err != nil {
		return "00000000000000000000000000000000"
	}
	return strings.ToUpper(raw)
}

func edgeTimestamp() string {
	return time.Now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT+0000 (Coordinated Universal Time)")
}

func parseEdgeHeaders(raw string) map[string]string {
	out := make(map[string]string, 4)
	for _, line := range strings.Split(raw, "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return out
}
