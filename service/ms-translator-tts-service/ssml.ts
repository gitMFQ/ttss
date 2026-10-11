/** SSML constructor matching the reference Microsoft Translator plugin. */

export interface MsSSMLOptions {
    voice: string
    lang?: string
    rate?: number
    pitch?: number
    volume?: number
    style?: string
    role?: string
    styleDegree?: number
    langSkill?: string
    omni?: boolean
}

function escapeXML(text: string): string {
    return text
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&apos;')
}

export function extractLocale(voiceName: string): string {
    const match = voiceName.match(/^([a-z]{2,3}-[A-Za-z]{2,4}(?:-[A-Za-z]{2,4})?)/)
    return match ? match[1] : 'zh-CN'
}

export function buildSSML(text: string, options: MsSSMLOptions): string {
    const lang = escapeXML(options.lang || extractLocale(options.voice))
    let textContent = escapeXML(text)
    if (options.langSkill) {
        textContent = `<lang xml:lang="${escapeXML(options.langSkill)}">${textContent}</lang>`
    }

    // Omni voices should not be stacked with express-as/prosody (reference plugin behavior).
    let voiceBody: string
    if (options.omni) {
        voiceBody = textContent
    } else {
        const rate = Math.round(options.rate ?? 0)
        const pitch = Math.round(options.pitch ?? 0)
        const volume = Math.round(options.volume ?? 100)
        const style = escapeXML(options.style || 'general')
        const role = escapeXML(options.role || 'default')
        const degree = Math.max(0, Math.min(2, options.styleDegree ?? 1)).toFixed(2)
        voiceBody = `<mstts:express-as style="${style}" styledegree="${degree}" role="${role}">` +
            `<prosody rate="${rate}%" pitch="${pitch}%" volume="${volume}%">${textContent}</prosody>` +
            `</mstts:express-as>`
    }

    return `<speak xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="http://www.w3.org/2001/mstts" xmlns:emo="http://www.w3.org/2009/10/emotionml" version="1.0" xml:lang="${lang}">` +
        `<voice name="${escapeXML(options.voice)}">${voiceBody}</voice></speak>`
}
