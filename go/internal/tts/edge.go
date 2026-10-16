package tts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	trustedClientToken = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	chromiumVersion    = "157.0.4329.0"
	voicesListURL      = "https://speech.platform.bing.com/consumer/speech/synthesize/readaloud/voices/list"
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
	resp, err := s.httpClient.Do(req)
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

func (s *Service) edgeSynthesize(ctx context.Context, text string, options Options) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	requestID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	connectionID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	secMSGEC := generateSecMSGEC(time.Now())
	u := url.URL{Scheme: "wss", Host: "speech.platform.bing.com", Path: "/consumer/speech/synthesize/readaloud/edge/v1"}
	query := u.Query()
	query.Set("TrustedClientToken", trustedClientToken)
	query.Set("Sec-MS-GEC", secMSGEC)
	query.Set("Sec-MS-GEC-Version", "1-"+chromiumVersion)
	query.Set("ConnectionId", connectionID)
	u.RawQuery = query.Encode()

	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second
	header := http.Header{}
	header.Set("Origin", "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold")
	header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Mobile Safari/537.36 EdgA/157.0.0.0")
	conn, resp, err := dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("connect Edge TTS: %w (HTTP %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("connect Edge TTS: %w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(32 << 20)
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(70 * time.Second)) })

	config := map[string]any{"context": map[string]any{"synthesis": map[string]any{"audio": map[string]any{
		"metadataoptions": map[string]string{"sentenceBoundaryEnabled": "false", "wordBoundaryEnabled": "false"},
		"outputFormat":    EdgeOutputFormat,
	}}}}
	configJSON, _ := json.Marshal(config)
	configFrame := fmt.Sprintf("X-Timestamp:%s\r\nContent-Type:application/json; charset=utf-8\r\nPath:speech.config\r\n\r\n%s", edgeTimestamp(), configJSON)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(configFrame)); err != nil {
		return nil, fmt.Errorf("send Edge speech configuration: %w", err)
	}
	ssml := buildEdgeSSML(text, options)
	ssmlFrame := fmt.Sprintf("X-Timestamp:%s\r\nX-RequestId:%s\r\nContent-Type:application/ssml+xml\r\nPath:ssml\r\n\r\n%s", edgeTimestamp(), requestID, ssml)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(ssmlFrame)); err != nil {
		return nil, fmt.Errorf("send Edge SSML: %w", err)
	}

	var audio []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		kind, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("read Edge TTS response: %w", err)
		}
		switch kind {
		case websocket.BinaryMessage:
			if len(message) < 2 {
				continue
			}
			headerLength := int(binary.BigEndian.Uint16(message[:2]))
			if headerLength < 1 || len(message) < headerLength+2 {
				continue
			}
			headers := parseEdgeHeaders(string(message[2 : 2+headerLength]))
			if headers["x-requestid"] == requestID {
				audio = append(audio, message[2+headerLength:]...)
			}
		case websocket.TextMessage:
			headerEnd := strings.Index(string(message), "\r\n\r\n")
			if headerEnd < 0 {
				continue
			}
			headers := parseEdgeHeaders(string(message[:headerEnd]))
			if strings.EqualFold(headers["path"], "turn.end") && headers["x-requestid"] == requestID {
				if len(audio) == 0 {
					return nil, fmt.Errorf("Edge TTS returned empty audio")
				}
				return audio, nil
			}
		case websocket.CloseMessage:
			return nil, io.EOF
		}
	}
}

func buildEdgeSSML(text string, options Options) string {
	return `<speak xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="http://www.w3.org/2001/mstts" xmlns:emo="http://www.w3.org/2009/10/emotionml" version="1.0" xml:lang="en-US"><voice name="` + escapeXML(options.Voice) + `"><prosody volume="` + fmt.Sprintf("%g%%", options.Volume) + `" rate="` + fmt.Sprintf("%g%%", options.Rate) + `" pitch="` + fmt.Sprintf("%g%%", options.Pitch) + `">` + escapeXML(text) + `</prosody></voice></speak>`
}

func generateSecMSGEC(now time.Time) string {
	const windowsEpochSeconds int64 = 11644473600
	const quantum int64 = 3000000000
	ticks := (now.Unix() + windowsEpochSeconds) * 10000000
	rounded := ticks - ticks%quantum
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d%s", rounded, trustedClientToken)))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func randomHex(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func edgeTimestamp() string {
	return time.Now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT-0700 (MST)")
}

func parseEdgeHeaders(raw string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(raw, "\r\n") {
		if i := strings.IndexByte(line, ':'); i > 0 {
			out[strings.ToLower(strings.TrimSpace(line[:i]))] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}
