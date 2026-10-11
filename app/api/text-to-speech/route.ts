import { EdgeTTSService } from "@/service/edge-tts-service"
import {
    DEFAULT_OUTPUT_FORMAT,
    MsTranslatorTTSService,
    OUTPUT_FORMATS,
    OUTPUT_REGIONS,
    getStableOutputFormat,
    outputFormatContentType,
} from "@/service/ms-translator-tts-service"
import { TTSOptions } from "@/service/tts-service"

export const dynamic = "force-dynamic"

class RequestValidationError extends Error {}

export async function GET(request: Request) {
    try {
        const authorization = request.headers.get('authorization')
        const requiredToken = process.env.MS_RA_FORWARDER_TOKEN || process.env.TOKEN
        if (requiredToken && authorization !== 'Bearer ' + requiredToken) {
            return new Response('Unauthorized', { status: 401 })
        }

        const { searchParams } = new URL(request.url)
        const text = String(searchParams.get('text') ?? '')
        if (!text) {
            return Response.json({ error: 'Text is required' }, { status: 400 })
        }
        const voice = String(searchParams.get('voice') ?? '')
        if (!voice) {
            return Response.json({ error: 'Voice is required' }, { status: 400 })
        }

        const serviceType = String(searchParams.get('service') ?? 'edge').toLowerCase()
        const isMs = ['ms', 'mstranslator', 'microsoft'].includes(serviceType)
        if (!isMs && serviceType !== 'edge') {
            return Response.json({ error: 'Unsupported service. Use edge or ms.' }, { status: 400 })
        }

        const parseNumberParam = (name: string, defaultValue: number, min: number, max: number) => {
            const raw = searchParams.get(name)
            if (raw === null) return defaultValue
            const value = Number(raw)
            if (!Number.isFinite(value) || value < min || value > max) {
                throw new RequestValidationError(`Invalid ${name} value`)
            }
            return value
        }
        const parseBooleanParam = (name: string, defaultValue: boolean) => {
            const raw = searchParams.get(name)
            if (raw === null) return defaultValue
            if (/^(1|true|yes)$/i.test(raw)) return true
            if (/^(0|false|no)$/i.test(raw)) return false
            throw new RequestValidationError(`Invalid ${name} value`)
        }

        const rate = parseNumberParam('rate', 0, -100, 100)
        const pitch = parseNumberParam('pitch', 0, -100, 100)
        // UI semantics: volume 0 is neutral for both services; preserve legacy API's
        // absent-value default for Edge, while MS uses the slider's center.
        const volume = parseNumberParam('volume', isMs ? 0 : 100, -100, 100)
        const personality = searchParams.get('style') ?? searchParams.get('personality') ?? undefined
        const role = searchParams.get('role') ?? undefined
        const styleDegree = parseNumberParam('styleDegree', 1, 0, 2)
        const safeMode = searchParams.has('safeMode')
            ? parseBooleanParam('safeMode', true)
            : parseBooleanParam('msSafeMode', true)
        const omni = parseBooleanParam('omni', false)
        const langSkill = searchParams.get('langSkill') ?? undefined
        const autoStyleRules = searchParams.get('autoStyleRules') ?? searchParams.get('as') ?? undefined
        const format = searchParams.get('format') || DEFAULT_OUTPUT_FORMAT
        const region = searchParams.get('region') || 'auto'

        const options: TTSOptions = {
            voice,
            volume,
            rate,
            pitch,
            personality,
            role,
            styleDegree,
            safeMode,
            omni,
            langSkill,
            autoStyleRules,
            format: isMs ? format : undefined,
            region: isMs ? region : undefined,
        }

        let service: EdgeTTSService | MsTranslatorTTSService
        let contentType = 'audio/mpeg'
        if (isMs) {
            if (!OUTPUT_FORMATS.includes(format as typeof OUTPUT_FORMATS[number])) {
                return Response.json({ error: 'Unsupported output format' }, { status: 400 })
            }
            if (!OUTPUT_REGIONS.some(item => item.value === region)) {
                return Response.json({ error: 'Unsupported region' }, { status: 400 })
            }
            service = new MsTranslatorTTSService()
            const effectiveFormat = getStableOutputFormat(format, omni, safeMode)
            contentType = outputFormatContentType(effectiveFormat)
        } else {
            service = new EdgeTTSService()
        }

        const speech = await service.convert(text, options)
        return new Response(new Blob([speech.audio], { type: contentType }), {
            status: 200,
            headers: { 'Content-Type': contentType },
        })
    } catch (error) {
        if (error instanceof RequestValidationError) {
            return Response.json({ error: error.message }, { status: 400 })
        }
        console.error('textToSpeach error', error)
        console.error('Full stack', (error as Error).stack)
        return Response.json({ error: (error as Error).message }, { status: 500 })
    }
}
