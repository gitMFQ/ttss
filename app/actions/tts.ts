'use server'

import { EdgeTTSService } from '@/service/edge-tts-service'
import { MsTranslatorTTSService } from '@/service/ms-translator-tts-service'
import { TTSOptions, Voice } from '@/service/tts-service'
import Keyv from 'keyv'

const voicesCache = new Keyv<Voice[]>(undefined, {
    ttl: 1000 * 60 * 60, // 1小时缓存
})

async function getVoices(serviceType?: string) {
    const cacheKey = `voices:${serviceType ?? 'edge'}`
    let voices: Voice[] | undefined = await voicesCache.get(cacheKey)

    if (!voices) {
        const service = serviceType === 'ms' ? new MsTranslatorTTSService() : new EdgeTTSService()
        voices = await service.fetchVoices()
        await voicesCache.set(cacheKey, voices)
    }
    return voices
}

export async function listLocales(serviceType?: string) {
    const voices = await getVoices(serviceType)
    return voices?.map(voice => voice.locale).filter((value, index, self) => self.indexOf(value) === index) ?? []
}

export async function listVoices(locale?: string, serviceType?: string) {
    const voices = await getVoices(serviceType)
    if (locale) {
        return voices?.filter(voice => voice.locale === locale) ?? []
    }
    return voices ?? []
}

export async function textToSpeach(text: string, options: TTSOptions, serviceType?: string) {
    try {
        const service = serviceType === 'ms' ? new MsTranslatorTTSService() : new EdgeTTSService()
        const data = await service.convert(text, options)
        const base64Audio = Buffer.from(data.audio).toString('base64')
        return base64Audio
    } catch (error) {
        console.error('textToSpeach error', error)
        throw error
    }
}
