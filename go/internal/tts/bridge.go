package tts

import (
	"net/url"
	"regexp"
	"strings"
)

type emotionBridge struct {
	text      string
	style     string
	hasBridge bool
}

var (
	bridgeTags        = regexp.MustCompile(`(?is)\[\[\s*(?:emo|emotion|ms_style|azure_style|mstts_style|mimo_ctx|mimo_nl|mimo_tag|ms_ctx|ms_instruction|aliyun_ctx|aliyun_instruction|qwen_instr|cosy_instr)\s*:\s*([\s\S]*?)\]\]`)
	allDoubleTags     = regexp.MustCompile(`(?is)\[\[[\s\S]*?\]\]`)
	privateSingleTags = regexp.MustCompile(`(?i)\[\s*(?:emo|emotion|mimo_[^\]]+|ms_[^\]]+|azure_[^\]]+|mstts_[^\]]+|重音标记|短停顿|声线收紧|声音放稳|轻微上扬|轻微下沉|音色稍亮|句末干净停止|语速正常偏稳)[^\]]*\]`)
	leadingParen      = regexp.MustCompile(`^\s*[（(]([^）)]{1,80})[）)]\s*`)
	emotionRules      = []struct {
		pattern *regexp.Regexp
		emotion string
	}{
		{regexp.MustCompile(`开心|高兴|喜悦|愉快|欢快|happy|joy|cheer`), "happy"},
		{regexp.MustCompile(`兴奋|激动|excited|excitement`), "excited"},
		{regexp.MustCompile(`伤心|悲伤|难过|低落|压抑|孤独|哭|哭腔|sad|depress|lonely|cry`), "sad"},
		{regexp.MustCompile(`生气|愤怒|恼怒|暴怒|怒|angry|rage|furious`), "angry"},
		{regexp.MustCompile(`害怕|恐惧|惊恐|惊慌|慌张|fear|terrified|panic`), "fear"},
		{regexp.MustCompile(`紧张|急促|焦虑|不安|压迫|危机|tension|tense|anxious|nervous`), "tension"},
		{regexp.MustCompile(`惊讶|震惊|错愕|诧异|surprise`), "surprised"},
		{regexp.MustCompile(`冷淡|冷漠|疏离|平直|克制|严肃|庄重|肃穆|serious|cold|solemn`), "serious"},
		{regexp.MustCompile(`温柔|柔和|安抚|疼爱|亲昵|含蓄|羞涩|tender|gentle|affection|shy|soft`), "gentle"},
		{regexp.MustCompile(`轻快|俏皮|调侃|玩笑|playful|teasing|joke`), "playful"},
		{regexp.MustCompile(`命令|威严|警告|威胁|强势|command|threat`), "commanding"},
		{regexp.MustCompile(`希望|期待|释然|放松|hope|relief|relieved`), "relieved"},
		{regexp.MustCompile(`耳语|低语|whisper`), "whisper"},
	}
	styleCandidates = map[string][]string{
		"neutral": {"general"}, "happy": {"cheerful", "hopeful", "calm"}, "excited": {"excited", "cheerful"},
		"sad": {"sad", "depressed", "calm"}, "angry": {"angry", "disgruntled", "unfriendly", "serious"},
		"fear": {"fearful", "terrified", "sad"}, "tension": {"fearful", "serious", "angry"},
		"surprised": {"surprised", "excited"}, "serious": {"serious", "calm", "narration-relaxed"},
		"gentle": {"gentle", "affectionate", "calm", "hopeful"}, "playful": {"cheerful", "excited"},
		"commanding": {"angry", "serious", "unfriendly"}, "relieved": {"calm", "hopeful", "cheerful"},
		"whisper": {"whispering", "calm"},
	}
	styleAliases = map[string]string{
		"happy": "cheerful", "joy": "cheerful", "fear": "fearful", "whisper": "whispering", "playful": "cheerful",
		"commanding": "serious", "relieved": "hopeful", "friendly": "cheerful", "positive": "cheerful", "warm": "gentle",
		"lively": "cheerful", "passion": "excited", "sunshine": "cheerful", "humorous": "chat",
		"默认": "general", "通用": "general", "中性": "general", "旁白": "general", "愉快": "cheerful", "愉悦": "cheerful",
		"开心": "cheerful", "轻快": "cheerful", "兴奋": "excited", "充满希望": "hopeful", "平静": "calm", "温柔": "gentle",
		"亲昵": "affectionate", "聊天": "chat", "严肃": "serious", "抒情": "lyrical", "悲伤": "sad", "伤心": "sad",
		"生气": "angry", "愤怒": "angry", "恐惧": "fearful", "害怕": "fearful", "紧张": "fearful", "惊恐": "terrified",
		"惊讶": "surprised", "不满": "disgruntled", "冷淡": "unfriendly", "耳语": "whispering", "轻松旁白": "narration-relaxed",
		"客服": "customer-service", "语音助手": "voice-assistant", "安慰": "comforting",
	}
	knownStyles = []string{"general", "cheerful", "excited", "sad", "angry", "fearful", "terrified", "surprised", "serious", "calm", "gentle", "affectionate", "whispering", "disgruntled", "unfriendly", "hopeful", "lyrical", "narration-relaxed", "chat", "customer-service", "voice-assistant", "comforting"}
)

func resolveEmotionBridge(input, selectedStyle string, supportedStyles []string, rules string, safeMode bool) emotionBridge {
	text := strings.TrimSpace(stripHTMLAndDecode(input))
	var emotion, explicitStyle, cue string
	text = bridgeTags.ReplaceAllStringFunc(text, func(tag string) string {
		parts := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(tag, "[["), "]]"), ":", 2)
		if len(parts) != 2 {
			return ""
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := decodeBridgeValue(parts[1])
		switch key {
		case "emo", "emotion":
			if emotion == "" {
				emotion = value
			}
		case "ms_style", "azure_style", "mstts_style":
			if explicitStyle == "" {
				explicitStyle = value
			}
		default:
			cue += " " + value
		}
		return ""
	})
	text = allDoubleTags.ReplaceAllString(text, "")
	text = privateSingleTags.ReplaceAllString(text, "")
	for i := 0; i < 4; i++ {
		match := leadingParen.FindStringSubmatchIndex(text)
		if len(match) < 4 {
			break
		}
		value := strings.TrimSpace(text[match[2]:match[3]])
		if !looksLikeCue(value) {
			break
		}
		cue += " " + value
		text = text[match[1]:]
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = regexp.MustCompile(`[ \t]+`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)
	if text == "" {
		text = "。"
	}

	supported := map[string]bool{"general": true}
	for _, value := range supportedStyles {
		if value = normalizeStyle(value); value != "" {
			supported[value] = true
		}
	}
	hasBridge := emotion != "" || strings.TrimSpace(cue) != "" || explicitStyle != ""
	style := "general"
	if hasBridge {
		if direct := supportedStyle(explicitStyle, supported); direct != "" {
			style = direct
		} else {
			style = styleFromEmotion(firstNonempty(emotion, cue, explicitStyle), supported)
		}
	} else if !safeMode {
		selected := strings.TrimSpace(selectedStyle)
		if selected != "" && !strings.EqualFold(selected, "auto") {
			style = valueOrGeneral(supportedStyle(selected, supported))
		}
		if strings.EqualFold(selected, "auto") {
			style = valueOrGeneral(supportedStyle(matchAutoRules(text, rules), supported))
		}
	}
	return emotionBridge{text: text, style: style, hasBridge: hasBridge}
}

func decodeBridgeValue(value string) string {
	decoded, err := url.QueryUnescape(strings.ReplaceAll(value, "+", "%2B"))
	if err != nil {
		decoded = value
	}
	return strings.TrimSpace(strings.ReplaceAll(decoded, "+", " "))
}

func normalizeEmotion(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`^emo(?:tion)?\s*[：:]\s*`).ReplaceAllString(value, "")
	for _, rule := range emotionRules {
		if rule.pattern.MatchString(value) {
			return rule.emotion
		}
	}
	if regexp.MustCompile(`^(neutral|general|none|无|中性|平静|旁白|narration)$`).MatchString(value) {
		return "neutral"
	}
	return value
}

func normalizeStyle(value string) string {
	style := strings.ToLower(strings.TrimSpace(value))
	if alias, ok := styleAliases[style]; ok {
		return alias
	}
	for _, candidate := range knownStyles {
		if strings.Contains(style, candidate) {
			return candidate
		}
	}
	return ""
}

func supportedStyle(value string, supported map[string]bool) string {
	candidate := normalizeStyle(value)
	if candidate == "" {
		candidate = strings.ToLower(strings.TrimSpace(value))
	}
	if candidate != "" && supported[candidate] {
		return candidate
	}
	return ""
}

func styleFromEmotion(emotion string, supported map[string]bool) string {
	normalized := normalizeEmotion(emotion)
	candidates := styleCandidates[normalized]
	if len(candidates) == 0 {
		candidates = []string{"general"}
	}
	for _, candidate := range candidates {
		if supported[candidate] {
			return candidate
		}
	}
	return "general"
}

func matchAutoRules(text, rules string) string {
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		open, close := strings.Index(line, "["), strings.LastIndex(line, "]")
		if open <= 0 || close <= open {
			continue
		}
		style := normalizeStyle(line[:open])
		for _, term := range strings.Split(line[open+1:close], "|") {
			if term = strings.TrimSpace(term); style != "" && term != "" && strings.Contains(text, term) {
				return style
			}
		}
	}
	return ""
}

func looksLikeCue(value string) bool {
	if value == "" || len([]rune(value)) > 80 {
		return false
	}
	if regexp.MustCompile(`emo|emotion|mimo|语气|语调|情绪|声音|声线|冷淡|克制|低落|紧张|慌张|害怕|愤怒|生气|开心|温柔|平直|急促|压迫|哽咽|哭腔|耳语|低语|停顿|重音|上扬|下沉|严肃|庄重|轻快|俏皮|悲伤|惊讶|错愕`).MatchString(value) {
		return true
	}
	return regexp.MustCompile(`^[\p{Han}a-zA-Z，,、|｜\s]{1,24}$`).MatchString(value) && regexp.MustCompile(`冷|急|稳|慢|快|轻|重|悲|怒|怕|喜|哑|颤|低|高`).MatchString(value)
}

func stripHTMLAndDecode(value string) string {
	value = regexp.MustCompile(`(?i)<br\s*/?\s*>`).ReplaceAllString(value, "。")
	value = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, "")
	replacer := strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&apos;", "'")
	return replacer.Replace(value)
}

func valueOrGeneral(value string) string {
	if value == "" {
		return "general"
	}
	return value
}
