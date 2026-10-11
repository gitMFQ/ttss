export interface EmotionBridgeResult {
    text: string
    emotion: string
    cue: string
    style: string
    degree: number
    hasBridge: boolean
}

const EMOTION_ALIASES: Array<[RegExp, string]> = [
    [/^(neutral|general|none|无|中性|平静|旁白|narration)$/, 'neutral'],
    [/开心|高兴|喜悦|愉快|欢快|happy|joy|cheer/, 'happy'],
    [/兴奋|激动|excited|excitement/, 'excited'],
    [/伤心|悲伤|难过|低落|压抑|孤独|哭|哭腔|sad|depress|lonely|cry/, 'sad'],
    [/生气|愤怒|恼怒|暴怒|怒|angry|rage|furious/, 'angry'],
    [/害怕|恐惧|惊恐|惊慌|慌张|fear|terrified|panic/, 'fear'],
    [/紧张|急促|焦虑|不安|压迫|危机|tension|tense|anxious|nervous/, 'tension'],
    [/惊讶|震惊|错愕|诧异|surprise/, 'surprised'],
    [/冷淡|冷漠|疏离|平直|克制|严肃|庄重|肃穆|serious|cold|solemn/, 'serious'],
    [/温柔|柔和|安抚|疼爱|亲昵|含蓄|羞涩|tender|gentle|affection|shy|soft/, 'gentle'],
    [/轻快|俏皮|调侃|玩笑|playful|teasing|joke/, 'playful'],
    [/命令|威严|警告|威胁|强势|command|threat/, 'commanding'],
    [/希望|期待|释然|放松|hope|relief|relieved/, 'relieved'],
    [/耳语|低语|whisper/, 'whisper'],
]

const STYLE_CANDIDATES: Record<string, string[]> = {
    neutral: ['general'],
    happy: ['cheerful', 'hopeful', 'calm'],
    excited: ['excited', 'cheerful'],
    sad: ['sad', 'depressed', 'calm'],
    angry: ['angry', 'disgruntled', 'unfriendly', 'serious'],
    fear: ['fearful', 'terrified', 'sad'],
    tension: ['fearful', 'serious', 'angry'],
    surprised: ['surprised', 'excited'],
    serious: ['serious', 'calm', 'narration-relaxed'],
    gentle: ['gentle', 'affectionate', 'calm', 'hopeful'],
    playful: ['cheerful', 'excited'],
    commanding: ['angry', 'serious', 'unfriendly'],
    relieved: ['calm', 'hopeful', 'cheerful'],
    whisper: ['whispering', 'calm'],
}

const STYLE_ALIASES: Record<string, string> = {
    happy: 'cheerful', joy: 'cheerful', fear: 'fearful', whisper: 'whispering',
    playful: 'cheerful', commanding: 'serious', relieved: 'hopeful',
    friendly: 'cheerful', positive: 'cheerful', warm: 'gentle', lively: 'cheerful',
    passion: 'excited', sunshine: 'cheerful', humorous: 'chat',
    '默认': 'general', '通用': 'general', '中性': 'general', '旁白': 'general',
    '愉快': 'cheerful', '愉悦': 'cheerful', '开心': 'cheerful', '轻快': 'cheerful',
    '兴奋': 'excited', '充满希望': 'hopeful', '平静': 'calm', '温柔': 'gentle',
    '亲昵': 'affectionate', '聊天': 'chat', '严肃': 'serious', '抒情': 'lyrical',
    '悲伤': 'sad', '伤心': 'sad', '生气': 'angry', '愤怒': 'angry', '恐惧': 'fearful',
    '害怕': 'fearful', '紧张': 'fearful', '惊恐': 'terrified', '惊讶': 'surprised', '不满': 'disgruntled', '冷淡': 'unfriendly',
    '耳语': 'whispering', '轻松旁白': 'narration-relaxed', '客服': 'customer-service',
    '语音助手': 'voice-assistant', '安慰': 'comforting',
}

function trim(value: unknown): string {
    return String(value ?? '').trim()
}

function decodePayload(value: string): string {
    let result = String(value ?? '')
    try { result = decodeURIComponent(result) } catch { /* keep raw value */ }
    return result.replace(/\+/g, ' ').trim()
}

export function normalizeEmotion(value: string): string {
    const emotion = trim(value).toLowerCase().replace(/^emo(?:tion)?\s*[：:]\s*/i, '')
    for (const [pattern, normalized] of EMOTION_ALIASES) {
        if (pattern.test(emotion)) return normalized
    }
    return emotion
}

function normalizeStyle(value: string): string {
    const style = trim(value).toLowerCase()
    if (!style) return ''
    if (STYLE_ALIASES[style]) return STYLE_ALIASES[style]
    const known = [
        'general', 'cheerful', 'excited', 'sad', 'angry', 'fearful', 'terrified', 'surprised',
        'serious', 'calm', 'gentle', 'affectionate', 'whispering', 'disgruntled',
        'unfriendly', 'hopeful', 'lyrical', 'narration-relaxed', 'chat',
        'customer-service', 'voice-assistant', 'comforting',
    ]
    return known.find(item => style.includes(item)) ?? ''
}

function supportedStyle(style: string, supported: Set<string>): string | undefined {
    const raw = trim(style).toLowerCase()
    const candidate = normalizeStyle(style) || raw
    if (!candidate) return undefined
    return candidate === 'general' || supported.has(candidate) ? candidate : undefined
}

function styleFromEmotion(emotion: string, supported: Set<string>): string {
    const normalized = normalizeEmotion(emotion)
    const candidates = STYLE_CANDIDATES[normalized] ?? ['general']
    return candidates.find(style => style === 'general' || supported.has(style)) ?? 'general'
}

function stripHtmlAndDecode(text: string): string {
    return text
        .replace(/<br\s*\/?\s*>/gi, '。')
        .replace(/<[^>]+>/g, '')
        .replace(/&nbsp;/gi, ' ')
        .replace(/&amp;/gi, '&')
        .replace(/&lt;/gi, '<')
        .replace(/&gt;/gi, '>')
        .replace(/&quot;/gi, '"')
        .replace(/&#39;|&apos;/gi, "'")
}

function looksLikeCue(value: string): boolean {
    const cue = trim(value)
    if (!cue || cue.length > 80) return false
    if (/emo|emotion|mimo|语气|语调|情绪|声音|声线|冷淡|克制|低落|紧张|慌张|害怕|愤怒|生气|开心|温柔|平直|急促|压迫|哽咽|哭腔|耳语|低语|停顿|重音|上扬|下沉|严肃|庄重|轻快|俏皮|悲伤|惊讶|错愕/i.test(cue)) return true
    return /^[\u4e00-\u9fa5a-zA-Z，,、|｜\s]{1,24}$/.test(cue) && /冷|急|稳|慢|快|轻|重|悲|怒|怕|喜|哑|颤|低|高/.test(cue)
}

function matchAutoRules(text: string, rulesText?: string): string | undefined {
    if (!rulesText) return undefined
    for (const line of rulesText.split(/\r?\n/)) {
        const match = line.trim().match(/^(.+?)\[(.*)\]$/)
        if (!match) continue
        const style = normalizeStyle(match[1])
        const terms = match[2].split('|').map(term => term.trim()).filter(Boolean)
        if (style && terms.some(term => text.includes(term))) return style
    }
    return undefined
}

export function resolveEmotionBridge(input: string, options: {
    selectedStyle?: string
    supportedStyles?: string[]
    autoStyleRules?: string
    safeMode?: boolean
} = {}): EmotionBridgeResult {
    let text = stripHtmlAndDecode(String(input ?? ''))
    let emotion = ''
    let cue = ''
    let explicitStyle = ''

    text = text.replace(/\[\[\s*(?:emo|emotion)\s*:\s*([\s\S]*?)\]\]/ig, (_all, value: string) => {
        if (!emotion) emotion = decodePayload(value)
        return ''
    })
    text = text.replace(/\[\[\s*(?:ms_style|azure_style|mstts_style)\s*:\s*([\s\S]*?)\]\]/ig, (_all, value: string) => {
        if (!explicitStyle) explicitStyle = decodePayload(value)
        return ''
    })
    text = text.replace(/\[\[\s*(?:mimo_ctx|mimo_nl|mimo_tag|ms_ctx|ms_instruction|aliyun_ctx|aliyun_instruction|qwen_instr|cosy_instr)\s*:\s*([\s\S]*?)\]\]/ig, (_all, value: string) => {
        cue += ` ${decodePayload(value)}`
        return ''
    })
    // Never send internal bridge tags to TTS as spoken text.
    text = text.replace(/\[\[[\s\S]*?\]\]/g, '')
    text = text.replace(/\[\s*(?:emo|emotion|mimo_[^\]]+|ms_[^\]]+|azure_[^\]]+|mstts_[^\]]+|重音标记|短停顿|声线收紧|声音放稳|轻微上扬|轻微下沉|音色稍亮|句末干净停止|语速正常偏稳)[^\]]*\]/ig, '')

    for (let index = 0; index < 4; index++) {
        const match = text.match(/^\s*[（(]([^）)]{1,80})[）)]\s*/)
        if (!match || !looksLikeCue(match[1])) break
        cue += ` ${match[1]}`
        text = text.slice(match[0].length)
    }

    text = text.replace(/\r\n?/g, '\n').replace(/[ \t]+/g, ' ').replace(/\n{3,}/g, '\n\n').trim()
    if (!text) text = '。'

    const supported = new Set<string>()
    for (const rawStyle of options.supportedStyles ?? []) {
        const value = trim(rawStyle).toLowerCase()
        if (!value) continue
        supported.add(normalizeStyle(value) || value)
    }
    supported.add('general')
    const hasBridge = Boolean(emotion || cue.trim() || explicitStyle)
    const selected = trim(options.selectedStyle)
    let style = 'general'

    if (hasBridge) {
        const direct = supportedStyle(explicitStyle, supported)
        if (direct) {
            style = direct
        } else {
            style = styleFromEmotion(emotion || cue || explicitStyle, supported)
        }
    } else if (!options.safeMode) {
        if (selected && selected.toLowerCase() !== 'auto') {
            style = supportedStyle(selected, supported) ?? 'general'
        } else if (selected.toLowerCase() === 'auto') {
            const ruleStyle = matchAutoRules(text, options.autoStyleRules)
            style = ruleStyle ? (supportedStyle(ruleStyle, supported) ?? 'general') : 'general'
        }
    }

    return {
        text,
        emotion: normalizeEmotion(emotion || cue),
        cue: cue.trim(),
        style,
        degree: hasBridge && style !== 'general' ? 0.75 : 1,
        hasBridge,
    }
}
