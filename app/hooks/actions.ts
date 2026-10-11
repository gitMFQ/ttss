import { useMutation, useQuery } from "@tanstack/react-query";
import { listVoices, listLocales, textToSpeach } from "../actions/tts";
import { TTSOptions } from "@/service/tts-service";

export function useVoices(locale?: string, serviceType?: string) {
    return useQuery({
        queryKey: ['list-voices', locale, serviceType ?? 'edge'],
        queryFn: () => listVoices(locale, serviceType),
    })
}

export function useVoiceLocale(voice: string) {
    const { data: voices } = useVoices()
    return voices?.find(v => v.value === voice)?.locale
}

export function useVoice(voice: string, serviceType?: string) {
    const { data: voices } = useVoices(undefined, serviceType)
    return voices?.find(v => v.value === voice)
}

export function useLocales(serviceType?: string) {
    return useQuery({
        queryKey: ['list-locales', serviceType ?? 'edge'],
        queryFn: () => listLocales(serviceType),
    })
}

export function useTextToSpeach() {
    return useMutation({
        mutationFn: async ({ text, options, serviceType }: { text: string, options: TTSOptions, serviceType?: string }) => { return await textToSpeach(text, options, serviceType) },
    })
}
