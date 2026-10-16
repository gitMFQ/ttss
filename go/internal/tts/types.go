package tts

// Voice is the common voice catalog shape returned by both providers.
type Voice struct {
	Value            string   `json:"value"`
	Label            string   `json:"label"`
	Locale           string   `json:"locale"`
	Gender           string   `json:"gender"`
	Format           string   `json:"format"`
	Personalities    []string `json:"personalities"`
	Roles            []string `json:"roles,omitempty"`
	SecondaryLocales []string `json:"secondaryLocales,omitempty"`
	VoiceType        string   `json:"voiceType,omitempty"`
	OmniCapable      bool     `json:"omniCapable,omitempty"`
}

// Options matches the public TTS query parameter names and slider semantics.
type Options struct {
	Voice          string  `json:"voice"`
	Volume         float64 `json:"volume"`
	Rate           float64 `json:"rate"`
	Pitch          float64 `json:"pitch"`
	Personality    string  `json:"personality"`
	Role           string  `json:"role"`
	StyleDegree    float64 `json:"styleDegree"`
	Region         string  `json:"region"`
	Format         string  `json:"format"`
	LangSkill      string  `json:"langSkill"`
	Omni           bool    `json:"omni"`
	SafeMode       *bool   `json:"safeMode"`
	AutoStyleRules string  `json:"autoStyleRules"`
}

func (o Options) IsSafeMode() bool {
	return o.SafeMode == nil || *o.SafeMode
}

const (
	DefaultOutputFormat = "audio-24khz-160kbitrate-mono-mp3"
	EdgeOutputFormat    = "audio-24khz-96kbitrate-mono-mp3"
)
