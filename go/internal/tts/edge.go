package tts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	chromiumVersion = "143.0.3650.75"
	chromiumMajor   = "143"
	// secMSGECVersion must carry the v2 prefix: the older v1 token format is now
	// rejected with HTTP 403 during the WebSocket handshake.
	secMSGECVersion = "2-" + chromiumVersion
	voicesListURL   = "https://speech.platform.bing.com/consumer/speech/synthesize/readaloud/voices/list"
	// edgeUserAgent presents as desktop Edge, matching upstream edge-tts headers.
	edgeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" +
		chromiumMajor + ".0.0.0 Safari/537.36 Edg/" + chromiumMajor + ".0.0.0"
)

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
	u, _ := url.Parse(voicesListURL)
	q := u.Query()
	q.Set("trustedclienttoken", trustedClientToken)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MS Read Aloud Forwarder)")
	resp, err := s.voiceClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Edge voices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, upstreamError("fetch Edge voices", resp)
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

// edgeSynthesize submits a turn to the shared connection pool. The pool keeps
// a small set of long-lived WebSockets open and pipelines multiple turns onto
// whichever slot has the smallest queue, cutting the per-request handshake
// overhead (1+ seconds per cold connection).
func (s *Service) edgeSynthesize(ctx context.Context, text string, options Options) ([]byte, error) {
	pool := s.pool()
	if pool == nil {
		return nil, errEdgeNoDialer
	}
	requestID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	// Give the whole request (queue wait included) a hard ceiling so a stalled
	// upstream can never pin an HTTP handler forever.
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
