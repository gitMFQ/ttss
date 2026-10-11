import {
    DEFAULT_OUTPUT_FORMAT,
    OUTPUT_FORMATS,
    OUTPUT_REGIONS,
    getStableOutputFormat,
    outputFormatContentType,
} from '@/service/ms-translator-tts-service'

export const dynamic = "force-dynamic"

export async function GET(request: Request) {
    try {
        const requiredToken = process.env.MS_RA_FORWARDER_TOKEN || process.env.TOKEN
        const { searchParams } = new URL(request.url)
        const token = searchParams.get('token')
        if (requiredToken && token !== requiredToken) {
            return new Response('Unauthorized', { status: 401 })
        }

        const voice = String(searchParams.get('voice') ?? '')
        if (!voice) {
            return Response.json({ error: 'Voice is required' }, { status: 400 })
        }
        const service = searchParams.get('service') || 'edge'
        const isMs = ['ms', 'mstranslator', 'microsoft'].includes(service.toLowerCase())
        if (!isMs && service.toLowerCase() !== 'edge') {
            return Response.json({ error: 'Unsupported service. Use edge or ms.' }, { status: 400 })
        }
        const parseNumberParam = (paramName: string, defaultValue: number, min: number, max: number) => {
            const paramValue = searchParams.get(paramName)
            if (paramValue === null) return defaultValue
            const num = Number(paramValue)
            if (!Number.isFinite(num) || num < min || num > max) {
                throw new Error(`Invalid ${paramName} value`)
            }
            return num
        }
        const volume = parseNumberParam('volume', isMs ? 0 : 100, -100, 100)
        const pitch = parseNumberParam('pitch', 0, -100, 100)
        const options: Record<string, string | number | undefined> = {
            voice,
            volume,
            pitch,
            personality: searchParams.get('personality') ?? undefined,
            style: searchParams.get('style') ?? undefined,
        }
        if (isMs) {
            const optionalKeys = ['role', 'styleDegree', 'region', 'format', 'langSkill', 'omni', 'safeMode', 'msSafeMode', 'autoStyleRules', 'as']
            for (const key of optionalKeys) {
                const value = searchParams.get(key)
                if (value !== null) options[key] = value
            }
            options.service = service
        } else if (searchParams.has('service')) {
            options.service = service
        }

        // URLSearchParams encodes voice names/styles safely. Keep Legado's dynamic
        // rate/text templates literal because the app evaluates them after import.
        const params = new URLSearchParams()
        for (const [key, value] of Object.entries(options)) {
            if (value != null && value !== '') params.set(key, String(value))
        }
        let queryString = params.toString()
        queryString += `${queryString ? '&' : ''}rate={{(speakSpeed - 10) * 2}}&text={{java.encodeURI(speakText)}}`
        const protocol = searchParams.get('protocol') || 'http'
        const host = request.headers.get('host')
        const baseUrl = `${protocol}://${host}/api/text-to-speech`
        const apiUrl = `${baseUrl}?${queryString}`

        let contentType = 'audio/mpeg'
        if (isMs) {
            const format = searchParams.get('format') || DEFAULT_OUTPUT_FORMAT
            const region = searchParams.get('region') || 'auto'
            if (!OUTPUT_FORMATS.includes(format as typeof OUTPUT_FORMATS[number])) {
                return Response.json({ error: 'Unsupported output format' }, { status: 400 })
            }
            if (!OUTPUT_REGIONS.some(item => item.value === region)) {
                return Response.json({ error: 'Unsupported region' }, { status: 400 })
            }
            const safeModeRaw = searchParams.get('safeMode') ?? searchParams.get('msSafeMode') ?? 'true'
            const safeMode = !/^(0|false|no)$/i.test(safeModeRaw)
            const omni = /^(1|true|yes)$/i.test(searchParams.get('omni') || '')
            contentType = outputFormatContentType(getStableOutputFormat(format, omni, safeMode))
        }
        const data = {
            name: voice,
            contentType,
            id: Date.now(),
            loginCheckJs: '',
            loginUi: '',
            loginUrl: '',
            url: apiUrl,
            header: JSON.stringify(requiredToken ? { Authorization: 'Bearer ' + requiredToken } : {}),
        }
        return Response.json(data)
    } catch (error) {
        const message = (error as Error).message
        const status = message.startsWith('Invalid ') ? 400 : 500
        return Response.json({ error: message }, { status })
    }
}
