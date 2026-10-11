import {
    getTranslatorToken,
    listVoices,
    synthesize,
    DEFAULT_OUTPUT_FORMAT,
    getStableOutputFormat,
    MsVoiceInfo,
    normalizeVoiceName,
} from './client'
import { buildSSML, extractLocale } from './ssml'
import { resolveEmotionBridge } from './emotion-bridge'
import { Speech, TTSOptions, TTSService, Voice } from '../tts-service'

export {
    DEFAULT_OUTPUT_FORMAT,
    OUTPUT_FORMATS,
    OUTPUT_REGIONS,
    getTranslatorToken,
    normalizeVoiceName,
    outputFormatContentType,
    getStableOutputFormat,
    resolveServiceRegion,
} from './client'
export { buildSSML, extractLocale } from './ssml'
export { resolveEmotionBridge } from './emotion-bridge'

const VOICE_CACHE_TTL = 60 * 60 * 1000
let voiceCatalogCache: { voices: MsVoiceInfo[]; expiresAt: number } | null = null

async function getVoiceCatalog(): Promise<MsVoiceInfo[]> {
    if (voiceCatalogCache && voiceCatalogCache.expiresAt > Date.now()) {
        return voiceCatalogCache.voices
    }
    const voices = await listVoices()
    const filtered = voices.filter(info => {
        const status = (info.Status || '').toLowerCase()
        return Boolean(info.ShortName && info.Locale) && !/deprecated|retired/.test(status)
    })
    voiceCatalogCache = { voices: filtered, expiresAt: Date.now() + VOICE_CACHE_TTL }
    return filtered
}

function isOmniCapable(info: MsVoiceInfo | undefined, shortName: string): boolean {
    const name = (shortName || '').toLowerCase()
    const type = (info?.VoiceType || '').toLowerCase()
    return /neural$/i.test(shortName)
        && !name.includes('multilingual')
        && !name.includes('dragonhd')
        && !name.includes('hdflash')
        && !name.includes('neuralhd')
        && (!type || type === 'neural')
}

function toOmniVoiceName(shortName: string): string {
    return shortName.replace(/neural$/i, '') + ':DragonHDOmniLatestNeural'
}

function toVoice(info: MsVoiceInfo): Voice {
    return {
        value: info.ShortName,
        label: info.LocalName || info.ShortName,
        locale: info.Locale,
        gender: info.Gender,
        format: DEFAULT_OUTPUT_FORMAT,
        personalities: info.StyleList?.length ? info.StyleList : (info.VoiceTag?.VoicePersonalities ?? []),
        roles: info.RolePlayList ?? [],
        secondaryLocales: info.SecondaryLocaleList ?? [],
        voiceType: info.VoiceType,
        omniCapable: isOmniCapable(info, info.ShortName),
    }
}

function clamp(value: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, value))
}

/**
 * Complete Microsoft Translator TTS implementation based on the supplied plugin:
 * regions/formats, style/role/degree, language skills, Omni voice conversion,
 * safe mode and emotion/context bridge tags.
 */
export class MsTranslatorTTSService implements TTSService {
    async convert(text: string, options: TTSOptions): Promise<Speech> {
        const safeMode = options.safeMode ?? true
        const shortName = normalizeVoiceName(options.voice)
        const catalog = await getVoiceCatalog()
        const voiceInfo = catalog.find(info => info.ShortName.toLowerCase() === shortName.toLowerCase())
        const omni = Boolean(options.omni) && isOmniCapable(voiceInfo, shortName)
        const finalVoice = omni ? toOmniVoiceName(shortName) : shortName
        const region = options.region || 'auto'
        const format = getStableOutputFormat(options.format, Boolean(options.omni), safeMode)

        const languageSkill = this.resolveLanguageSkill(options.langSkill, voiceInfo, omni)
        const supportedStyles = voiceInfo?.StyleList?.length
            ? voiceInfo.StyleList
            : (voiceInfo?.VoiceTag?.VoicePersonalities ?? [])
        const bridge = resolveEmotionBridge(text, {
            selectedStyle: options.personality,
            supportedStyles,
            autoStyleRules: options.autoStyleRules,
            safeMode,
        })

        // Reference slider semantics: neutral is 50/100. Project sliders are -100..100,
        // so zero maps to the neutral center and +/-100 maps to the endpoints.
        const speed50 = clamp(50 + (options.rate ?? 0) / 2, 0, 100)
        const pitch50 = clamp(50 + (options.pitch ?? 0) / 2, 0, 100)
        const volume50 = clamp(50 + (options.volume ?? 0) / 2, 0, 100)
        const ratePct = safeMode
            ? clamp(Math.round(speed50 - 50), -10, 10)
            : clamp(Math.round(speed50 - 50), -50, 50)
        const pitchPct = safeMode ? 0 : clamp(Math.round(pitch50 - 50), -30, 30)
        const volume = safeMode
            ? clamp(Math.round(50 + volume50), 80, 120)
            : clamp(Math.round(50 + volume50), 40, 160)

        const style = omni ? undefined : bridge.style
        const role = safeMode ? 'default' : (options.role || 'default')
        const styleDegree = safeMode ? (bridge.hasBridge && style !== 'general' ? bridge.degree : 1) :
            (bridge.hasBridge && style !== 'general' ? bridge.degree : (options.styleDegree ?? 1))
        const ssml = buildSSML(bridge.text, {
            voice: finalVoice,
            lang: extractLocale(shortName),
            rate: ratePct,
            pitch: pitchPct,
            volume,
            style,
            role,
            styleDegree,
            langSkill: languageSkill,
            omni,
        })
        const audio = await synthesize(ssml, { format, region, omni })
        return { audio, sentenceBoundaries: [], wordBoundaries: [] }
    }

    async fetchVoices(): Promise<Array<Voice>> {
        const catalog = await getVoiceCatalog()
        return catalog.map(toVoice)
    }

    private resolveLanguageSkill(
        requested: string | undefined,
        voice: MsVoiceInfo | undefined,
        omni: boolean,
    ): string | undefined {
        if (!requested) return undefined
        const tag = requested.trim()
        if (!tag) return undefined
        if (omni) {
            const supportedOmniLocales = [
                'zh-CN', 'zh-HK', 'zh-TW', 'en-US', 'en-GB', 'ja-JP', 'ko-KR',
                'fr-FR', 'de-DE', 'es-ES', 'it-IT', 'pt-BR', 'ru-RU',
            ]
            return supportedOmniLocales.includes(tag) ? tag : undefined
        }
        return voice?.SecondaryLocaleList?.includes(tag) ? tag : undefined
    }
}
