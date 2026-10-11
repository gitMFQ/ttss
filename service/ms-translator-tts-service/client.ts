import crypto from 'node:crypto'

/**
 * 微软翻译（Microsoft Translator Android App）免费通道的 Azure TTS 客户端。
 *
 * 原理（参考 ttsrv 微软翻译插件）：
 * 1. 用固定 HmacSHA256 密钥对 `MSTranslatorAndroidApp + URL + GMT日期 + UUID` 签名，
 *    生成 X-MT-Signature 请求头；
 * 2. POST https://dev.microsofttranslator.com/apps/endpoint?api-version=1.0
 *    换取 JSON { r: 区域, t: Azure TTS 访问令牌（JWT，约 10 分钟有效）}；
 * 3. 携带该令牌调用 https://{r}.tts.speech.microsoft.com/cognitiveservices/v1
 *    合成语音，无需任何 Azure 订阅密钥。
 */

const TRANSLATOR_ENDPOINT = 'https://dev.microsofttranslator.com/apps/endpoint?api-version=1.0'
const MT_SIGNATURE_SECRET_BASE64 = 'oik6PdDdMnOXemTbwvMn9de/h9lFnfBaCWbGMMZqqoSaQaqUOqjVGm5NqsmjcBI1x+sS9ugjB55HEJWRiFXYFw=='

export const DEFAULT_OUTPUT_FORMAT = 'audio-24khz-160kbitrate-mono-mp3'

/** 参考插件公开的输出格式；UI 默认选择 MP3，亦可选 Opus/Ogg/WAV 等。 */
export const OUTPUT_FORMATS = [
    'audio-16khz-32kbitrate-mono-mp3',
    'audio-16khz-64kbitrate-mono-mp3',
    'audio-16khz-128kbitrate-mono-mp3',
    'audio-24khz-48kbitrate-mono-mp3',
    'audio-24khz-96kbitrate-mono-mp3',
    'audio-24khz-160kbitrate-mono-mp3',
    'audio-48khz-96kbitrate-mono-mp3',
    'audio-48khz-192kbitrate-mono-mp3',
    'webm-16khz-16bit-mono-opus',
    'webm-24khz-16bit-mono-opus',
    'webm-24khz-16bit-24kbps-mono-opus',
    'ogg-16khz-16bit-mono-opus',
    'ogg-24khz-16bit-mono-opus',
    'ogg-48khz-16bit-mono-opus',
    'amr-wb-16000hz',
    'riff-8khz-8bit-mono-alaw',
    'riff-8khz-8bit-mono-mulaw',
    'riff-8khz-16bit-mono-pcm',
    'riff-16khz-16bit-mono-pcm',
    'riff-22050hz-16bit-mono-pcm',
    'riff-24khz-16bit-mono-pcm',
    'riff-44100hz-16bit-mono-pcm',
    'riff-48khz-16bit-mono-pcm',
] as const

export const OUTPUT_REGIONS = [
    { value: 'auto', label: '自动（令牌区域）' },
    { value: 'hd', label: '高清区域（HD）' },
    { value: 'eastasia', label: '东亚' },
    { value: 'southeastasia', label: '东南亚' },
    { value: 'northeurope', label: '北欧' },
    { value: 'westeurope', label: '西欧' },
    { value: 'westus', label: '美国西部' },
    { value: 'eastus', label: '美国东部' },
] as const

export function getStableOutputFormat(format: string | undefined, omni: boolean, safeMode: boolean): string {
    const requested = format || DEFAULT_OUTPUT_FORMAT
    if (safeMode) return DEFAULT_OUTPUT_FORMAT
    if (omni && /48khz|opus|amr|8khz|22050/i.test(requested)) return DEFAULT_OUTPUT_FORMAT
    return requested
}

export function outputFormatContentType(format: string): string {
    if (format.startsWith('webm-')) return 'audio/webm'
    if (format.startsWith('ogg-')) return 'audio/ogg'
    if (format.startsWith('riff-')) return 'audio/wav'
    if (format.startsWith('amr-')) return 'audio/amr'
    return 'audio/mpeg'
}

/**
 * 音色名归一化：Edge 列表用全名（Microsoft Server Speech Text to Speech Voice (zh-CN, XiaoxiaoNeural)），
 * 本通道只认短名（zh-CN-XiaoxiaoNeural）。已是短名则原样返回。
 */
export function normalizeVoiceName(voice: string): string {
    if (/^[a-z]{2,3}-[A-Za-z]/.test(voice)) {
        return voice
    }
    const match = voice.match(/\(([^,()]+),\s*([^()]+)\)/)
    if (match) {
        return `${match[1].trim()}-${match[2].trim()}`
    }
    return voice
}

export interface TranslatorToken {
    /** 区域，如 southeastasia */
    region: string
    /** Azure TTS 访问令牌（JWT） */
    token: string
    /** 本地过期时间戳（毫秒） */
    expiresAt: number
}

interface CachedToken extends TranslatorToken {
    /** 提前量：到期前 60 秒即视为过期 */
}

let cachedToken: TranslatorToken | null = null
let tokenPromise: Promise<TranslatorToken> | null = null

/** Java SimpleDateFormat("EEE, dd MMM yyyy HH:mm:ss", US) + GMT，再整体小写的等价实现 */
function gmtDateString(): string {
    // toUTCString(): "Mon, 12 Jan 2026 05:30:00 GMT" → 去掉时区后缀，转小写，补 GMT
    return new Date().toUTCString().replace(/\s*GMT$/i, '').toLowerCase() + 'GMT'
}

/** 生成 X-MT-Signature：MSTranslatorAndroidApp::HMAC::日期::UUID */
function buildMtSignature(): string {
    // 参考实现对 URL 做了 split('://')[1]，即对 "dev.microsofttranslator.com/apps/endpoint?api-version=1.0" 编码
    const pathWithQuery = TRANSLATOR_ENDPOINT.split('://')[1]
    const encoded = encodeURIComponent(pathWithQuery)
    const traceId = crypto.randomUUID().replaceAll('-', '')
    const date = gmtDateString()
    const raw = `MSTranslatorAndroidApp${encoded}${date}${traceId}`.toLowerCase()
    const hmac = crypto.createHmac('sha256', Buffer.from(MT_SIGNATURE_SECRET_BASE64, 'base64'))
        .update(raw, 'utf8')
        .digest('base64')
    return `MSTranslatorAndroidApp::${hmac}::${date}::${traceId}`
}

/** 从 JWT payload 的 exp 解析过期时间；解析失败则按 5 分钟保守处理 */
function parseExpiry(token: string): number {
    try {
        const payload = JSON.parse(Buffer.from(token.split('.')[1], 'base64').toString('utf8'))
        if (typeof payload.exp === 'number' && payload.exp > 0) {
            return payload.exp * 1000
        }
    } catch {
        // ignore
    }
    return Date.now() + 5 * 60 * 1000
}

/** 获取（带缓存的）微软翻译通道令牌；并发请求共享同一次获取 */
export async function getTranslatorToken(): Promise<TranslatorToken> {
    // 缓存有效期：真实过期前 60 秒
    if (cachedToken && cachedToken.expiresAt - 60_000 > Date.now()) {
        return cachedToken
    }
    if (tokenPromise) {
        return tokenPromise
    }
    tokenPromise = (async () => {
        const response = await fetch(TRANSLATOR_ENDPOINT, {
            method: 'POST',
            headers: {
                'Accept-Language': 'zh-Hans',
                'X-ClientVersion': '4.0.530a 5fe1dc6c',
                'X-UserId': '0f04d16a175c411e',
                'X-HomeGeographicRegion': 'zh-Hans-CN',
                'X-ClientTraceId': crypto.randomUUID(),
                'X-MT-Signature': buildMtSignature(),
                'User-Agent': 'okhttp/4.5.0',
                'Content-Type': 'application/json; charset=utf-8',
            },
            body: '',
            signal: AbortSignal.timeout(15_000),
        })
        if (!response.ok) {
            const body = await response.text().catch(() => '')
            throw new Error(`获取微软翻译令牌失败：HTTP ${response.status}${body ? '；' + body.slice(0, 300) : ''}`)
        }
        const data = await response.json() as { r?: unknown, t?: unknown }
        const region = typeof data.r === 'string' ? data.r : ''
        const token = typeof data.t === 'string' ? data.t : ''
        if (!region || !token) {
            throw new Error('微软翻译令牌响应格式异常：缺少 r/t 字段')
        }
        const result: TranslatorToken = {
            region,
            token,
            expiresAt: parseExpiry(token),
        }
        cachedToken = result
        return result
    })()
    try {
        return await tokenPromise
    } finally {
        tokenPromise = null
    }
}

/** 强制刷新令牌（收到 401 时调用） */
async function refreshTranslatorToken(): Promise<TranslatorToken> {
    cachedToken = null
    return getTranslatorToken()
}

/**
 * 合成语音。返回 MP3 音频的 ArrayBuffer。
 * 令牌 401 失效时自动刷新并重试一次。
 */
const HD_REGION_MAP: Record<string, string> = {
    eastasia: 'southeastasia',
    northeurope: 'westeurope',
    westus: 'eastus',
}

export function resolveServiceRegion(setting: string | undefined, tokenRegion: string, omni = false): string {
    let requested = (setting || 'auto').toLowerCase()
    if (omni && requested === 'auto') requested = 'hd'
    if (requested === 'auto') return tokenRegion
    if (requested === 'hd') return HD_REGION_MAP[tokenRegion] || tokenRegion
    if (omni && HD_REGION_MAP[requested]) return HD_REGION_MAP[requested]
    return OUTPUT_REGIONS.some(item => item.value === requested) ? requested : tokenRegion
}

export async function synthesize(
    ssml: string,
    options: { format?: string; region?: string; omni?: boolean } = {},
): Promise<ArrayBuffer> {
    const format = options.format || DEFAULT_OUTPUT_FORMAT
    const request = async (auth: TranslatorToken): Promise<Response> => {
        const region = resolveServiceRegion(options.region, auth.region, options.omni)
        const url = `https://${region}.tts.speech.microsoft.com/cognitiveservices/v1`
        return fetch(url, {
            method: 'POST',
            headers: {
                // 注意：此通道的 Authorization 直接放 JWT，不加 Bearer 前缀（与参考实现一致）
                'Authorization': auth.token,
                'Content-Type': 'application/ssml+xml',
                'X-Microsoft-OutputFormat': format,
                'User-Agent': 'okhttp/4.5.0',
            },
            body: ssml,
            signal: AbortSignal.timeout(60_000),
        })
    }

    let auth = await getTranslatorToken()
    let response = await request(auth)
    if (response.status === 401) {
        auth = await refreshTranslatorToken()
        response = await request(auth)
    }
    if (!response.ok) {
        const body = await response.text().catch(() => '')
        throw new Error(`微软翻译 TTS 合成失败：HTTP ${response.status}${body ? '；' + body.slice(0, 300) : ''}`)
    }
    return response.arrayBuffer()
}

export interface MsVoiceInfo {
    /** 完整名称，如 "Microsoft Server Speech Text to Speech Voice (zh-CN, XiaoxiaoNeural)" */
    Name: string
    /** 短名，如 "zh-CN-XiaoxiaoNeural" */
    ShortName: string
    Gender: string
    Locale: string
    LocalName?: string
    LocaleName?: string
    Status?: string
    VoiceType?: string
    StyleList?: string[]
    RolePlayList?: string[]
    SecondaryLocaleList?: string[]
    VoiceTag?: {
        ContentCategories?: string[]
        VoicePersonalities?: string[]
    }
    [key: string]: unknown
}

/**
 * 获取音色列表（该端点返回 900+ 音色，含 StyleList，信息比 Edge 免费端点更全）。
 * 与令牌相同通道鉴权（Authorization 直接放 JWT）。
 */
export async function listVoices(): Promise<MsVoiceInfo[]> {
    const request = async (auth: TranslatorToken): Promise<Response> => {
        const url = `https://${auth.region}.tts.speech.microsoft.com/cognitiveservices/voices/list`
        return fetch(url, {
            method: 'GET',
            headers: { 'Authorization': auth.token },
            signal: AbortSignal.timeout(15_000),
        })
    }

    let auth = await getTranslatorToken()
    let response = await request(auth)
    if (response.status === 401) {
        auth = await refreshTranslatorToken()
        response = await request(auth)
    }
    if (!response.ok) {
        const body = await response.text().catch(() => '')
        throw new Error(`获取微软翻译音色列表失败：HTTP ${response.status}${body ? '；' + body.slice(0, 300) : ''}`)
    }
    const data = await response.json()
    return Array.isArray(data) ? data as MsVoiceInfo[] : []
}
