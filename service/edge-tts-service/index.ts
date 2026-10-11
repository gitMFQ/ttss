import { EdgeTTSClient } from './client'
import { Speech, SpeechBoundary, TTSOptions, TTSService, Voice } from '../tts-service'
import { SSML } from '../ssml'
import { getFriendlyVoiceName } from './voice-map'
import { getFirendlyPersonalityName } from './personality-map'

interface EdgeTTSBoundaryData {
    Offset: number
    Duration: number
    text: {
        Text: string
        Length: number
    }
}

interface EdgeTTSMetadataEntry {
    Type: string
    Data: EdgeTTSBoundaryData
}


export class EdgeTTSService implements TTSService {

    async convert(text: string, options: TTSOptions): Promise<Speech> {
        const ssml = new SSML(text, options.voice, options.volume, options.rate, options.pitch)
        return await this.convertSSML(ssml.toString())
    }
    private async convertSSML(ssml: string): Promise<Speech> {
        const result = await EdgeTTSClient.convert(ssml, {
            format: "audio-24khz-96kbitrate-mono-mp3",
            sentenceBoundaryEnabled: false,
            wordBoundaryEnabled: false,
        })
        const sentenceBoundaries = (result.metadata as unknown as EdgeTTSMetadataEntry[]).filter(data => {
            return data["Type"] === "SentenceBoundary"
        }).map(data => {
            const sb: SpeechBoundary = {
                start: data["Data"]["Offset"] / 10000,
                end: data["Data"]["Offset"] / 10000 + data["Data"]["Duration"] / 10000,
                duration: data["Data"]["Duration"] / 10000,
                text: data["Data"]["text"]["Text"],
                length: String(data["Data"]["text"]["Length"]),
            }
            return sb
        })
        const wordBoundaries = (result.metadata as unknown as EdgeTTSMetadataEntry[]).filter(data => {
            return data["Type"] === "WordBoundary"
        }).map(data => {
            const sb: SpeechBoundary = {
                start: data["Data"]["Offset"] / 10000,
                end: data["Data"]["Offset"] / 10000 + data["Data"]["Duration"] / 10000,
                duration: data["Data"]["Duration"] / 10000,
                text: data["Data"]["text"]["Text"],
                length: String(data["Data"]["text"]["Length"]),
            }
            return sb
        })
        return {
            audio: result.audio,
            sentenceBoundaries: sentenceBoundaries,
            wordBoundaries: wordBoundaries
        }
    }
    async fetchVoices(): Promise<Array<Voice>> {
        const data = await EdgeTTSClient.voices() as Array<Record<string, unknown>>
        const voices = data.map((item) => {
            const voiceTag = item['VoiceTag'] as Record<string, unknown> | undefined
            const voice: Voice = {
                label: item['FriendlyName'] as string,
                gender: item['Gender'] as string,
                value: item['Name'] as string,
                locale: item['Locale'] as string,
                format: item['SuggestedCodec'] as string,
                personalities: (voiceTag?.['VoicePersonalities'] as string[] | undefined)?.map((personality: string) => {
                    return personality
                }) ?? [],

            }
            return voice
        })
        return voices
    }

}
