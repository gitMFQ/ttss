'use client'
import { useVoice, useVoices } from "@/app/hooks/actions"
import { useToast } from "@/components/shadcn/hooks/use-toast"
import { Button } from "@/components/shadcn/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/shadcn/ui/command"
import { Form, FormField, FormItem, FormMessage } from "@/components/shadcn/ui/form"
import { Label } from "@/components/shadcn/ui/label"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/shadcn/ui/popover"
import { Slider } from "@/components/shadcn/ui/slider"
import { Textarea } from "@/components/shadcn/ui/textarea"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/shadcn/ui/dialog"
import { getFirendlyPersonalityName } from "@/service/edge-tts-service/personality-map"
import { getFriendlyVoiceName } from "@/service/edge-tts-service/voice-map"
import { getLocaleFriendlyName } from "@/service/edge-tts-service/locale-map"
import { TTSOptions, TTSOptionsSchema } from "@/service/tts-service"
import { zodResolver } from "@hookform/resolvers/zod"
import * as PopoverPrimitive from "@radix-ui/react-popover"
import axios from "axios"
import clsx from "clsx"
import { Check, ChevronsUpDown, LoaderCircle, Smile, Speech, QrCode, Globe, Copy, Server } from "lucide-react"
import { HTMLAttributes, useMemo, useEffect, useState } from "react"
import { useForm } from "react-hook-form"
import { v4 as uuidv4 } from 'uuid'
import { z } from "zod"
import { useTTSContext } from "./tts-context"
import Image from "next/image"
import { useAuth } from "@/app/hooks/auth"
import QRCode from 'qrcode'

const TTSRequestSchame = z.object({
    options: TTSOptionsSchema,
    text: z.string(),
    service: z.enum(['edge', 'ms']).default('edge')
})

// 服务通道选项
const serviceOptions = [
    { value: 'edge', label: 'Edge 大声朗读', hint: '免费，不支持讲话风格' },
    { value: 'ms', label: '微软翻译', hint: '免费，支持讲话风格' },
] as const

const msRegions = [
    { value: 'auto', label: '自动（令牌区域）' }, { value: 'hd', label: '高清区域（HD）' },
    { value: 'eastasia', label: '东亚' }, { value: 'southeastasia', label: '东南亚' },
    { value: 'northeurope', label: '北欧' }, { value: 'westeurope', label: '西欧' },
    { value: 'westus', label: '美国西部' }, { value: 'eastus', label: '美国东部' },
] as const

const msOutputFormats = [
    'audio-16khz-32kbitrate-mono-mp3', 'audio-16khz-64kbitrate-mono-mp3', 'audio-16khz-128kbitrate-mono-mp3',
    'audio-24khz-48kbitrate-mono-mp3', 'audio-24khz-96kbitrate-mono-mp3', 'audio-24khz-160kbitrate-mono-mp3',
    'audio-48khz-96kbitrate-mono-mp3', 'audio-48khz-192kbitrate-mono-mp3',
    'webm-16khz-16bit-mono-opus', 'webm-24khz-16bit-mono-opus', 'webm-24khz-16bit-24kbps-mono-opus',
    'ogg-16khz-16bit-mono-opus', 'ogg-24khz-16bit-mono-opus', 'ogg-48khz-16bit-mono-opus', 'amr-wb-16000hz',
    'riff-8khz-8bit-mono-alaw', 'riff-8khz-8bit-mono-mulaw', 'riff-8khz-16bit-mono-pcm',
    'riff-16khz-16bit-mono-pcm', 'riff-22050hz-16bit-mono-pcm', 'riff-24khz-16bit-mono-pcm',
    'riff-44100hz-16bit-mono-pcm', 'riff-48khz-16bit-mono-pcm',
] as const

const omniLocales = ['zh-CN', 'zh-HK', 'zh-TW', 'en-US', 'en-GB', 'ja-JP', 'ko-KR', 'fr-FR', 'de-DE', 'es-ES', 'it-IT', 'pt-BR', 'ru-RU']

function formatLabel(format: string): string {
    return format.replace(/^audio-/, '').replace(/-mono-/, ' / ').replace(/-/g, ' ')
}

const msStyleNames: Record<string, string> = {
    general: '默认', cheerful: '愉快', excited: '兴奋', hopeful: '充满希望',
    calm: '平静', gentle: '温柔', affectionate: '亲昵', chat: '聊天',
    serious: '严肃', lyrical: '抒情', sad: '悲伤', angry: '愤怒',
    fearful: '恐惧', terrified: '惊恐', surprised: '惊讶',
    disgruntled: '不满', unfriendly: '冷淡', whispering: '耳语',
    'narration-relaxed': '轻松旁白', 'customer-service': '客服',
    'voice-assistant': '语音助手', comforting: '安慰',
}

function personalityLabel(value: string, service: string): string {
    return service === 'ms'
        ? (msStyleNames[value.toLowerCase()] ?? value)
        : getFirendlyPersonalityName(value)
}

export interface TTSWorkspaceProps extends Omit<HTMLAttributes<HTMLDivElement>, 'children'> {
    options?: TTSOptions
    text?: string
}

export default function TTSWorkspace({ ...props }: TTSWorkspaceProps) {
    const { toast } = useToast()

    const [selectedLocale, setSelectedLocale] = useState<string>('')
    const [qrCodeDataUrl, setQrCodeDataUrl] = useState<string>('')
    const [showQrDialog, setShowQrDialog] = useState(false)
    const { getToken } = useAuth()
    const { saveHistoryRecord: save } = useTTSContext()



    const form = useForm<z.infer<typeof TTSRequestSchame>>({
        resolver: zodResolver(TTSRequestSchame),
        defaultValues: {
            options: {
                voice: 'Microsoft Server Speech Text to Speech Voice (zh-CN, XiaoxiaoNeural)',
                safeMode: true,
                omni: false,
                region: 'auto',
                format: 'audio-24khz-160kbitrate-mono-mp3',
                styleDegree: 1,
            },
            text: '君不见黄河之水天上来，奔流到海不复回。',
            service: 'edge'
        }
    })

    const selectedService = form.watch('service') ?? 'edge'
    const watchedFormValues = form.watch()
    const voicesQuery = useVoices('', selectedService) // 根据通道加载音色列表

    // 切换通道时，若当前发音人在新通道不存在则重置为该地区的第一个
    useEffect(() => {
        const subscription = form.watch((_, { name }) => {
            if (name === 'service') {
                setSelectedLocale('')
                form.setValue('options.voice', '')
                form.setValue('options.personality', undefined)
                form.setValue('options.role', undefined)
                form.setValue('options.langSkill', undefined)
                form.setValue('options.omni', false)
            }
        })
        return () => subscription.unsubscribe()
    }, [form])

    // 从voicesQuery中提取所有可用的locales
    const availableLocales = useMemo(() => {
        if (!voicesQuery.data) return []
        const locales = Array.from(new Set(voicesQuery.data.map(voice => voice.locale)))
        return locales.sort()
    }, [voicesQuery.data])

    // 获取当前地区的发音人列表
    const currentLocaleVoices = useMemo(() => {
        if (!voicesQuery.data || !selectedLocale) return []
        return voicesQuery.data.filter(voice => voice.locale === selectedLocale)
    }, [voicesQuery.data, selectedLocale])

    // 设置默认选择中国大陆
    useEffect(() => {
        if (availableLocales.length > 0 && !selectedLocale) {
            const chinaLocale = availableLocales.find(locale => locale.includes('zh-CN')) || availableLocales[0]
            setSelectedLocale(chinaLocale)
        }
    }, [availableLocales, selectedLocale])

    // 地区切换时自动选择该地区的第一个发音人
    useEffect(() => {
        if (selectedLocale && currentLocaleVoices.length > 0) {
            const currentVoice = form.getValues('options.voice')
            // 检查当前选择的发音人是否属于当前地区
            const isCurrentVoiceInLocale = currentLocaleVoices.some(voice => voice.value === currentVoice)
            if (!isCurrentVoiceInLocale) {
                // 自动选择该地区的第一个发音人
                form.setValue('options.voice', currentLocaleVoices[0].value)
            }
        }
    }, [selectedLocale, currentLocaleVoices, form])


    const selectedVoice = form.watch('options.voice')
    const currentVoice = useVoice(selectedVoice, selectedService)
    const selectedSafeMode = form.watch('options.safeMode') ?? true
    const selectedOmni = form.watch('options.omni') ?? false
    const selectedPersonality = form.watch('options.personality')
    const languageSkillChoices = useMemo(() => {
        if (selectedService !== 'ms' || !currentVoice) return []
        if (selectedOmni) {
            const locales = [...new Set([currentVoice.locale, ...omniLocales, ...(voicesQuery.data ?? []).map(voice => voice.locale)])]
            return locales.map(value => ({ value, label: getLocaleFriendlyName(value) }))
        }
        const secondary = currentVoice.secondaryLocales ?? []
        return secondary.length
            ? [...new Set([currentVoice.locale, ...secondary])].map(value => ({ value, label: getLocaleFriendlyName(value) }))
            : []
    }, [selectedService, currentVoice, selectedOmni, voicesQuery.data])

    useEffect(() => {
        if (selectedService !== 'ms' || !currentVoice) return
        if (!currentVoice.omniCapable && form.getValues('options.omni')) {
            form.setValue('options.omni', false)
        }
        const selectedRole = form.getValues('options.role')
        if (selectedRole && selectedRole !== 'default' && !currentVoice.roles?.includes(selectedRole)) {
            form.setValue('options.role', undefined)
        }
        const selectedLanguage = form.getValues('options.langSkill')
        if (selectedLanguage && !languageSkillChoices.some(item => item.value === selectedLanguage)) {
            form.setValue('options.langSkill', undefined)
        } else if (!selectedLanguage && languageSkillChoices.length > 0) {
            form.setValue('options.langSkill', languageSkillChoices[0].value)
        }
    }, [selectedService, currentVoice, languageSkillChoices, form])

    const onSubmit = form.handleSubmit(async data => {
        try {
            if (!data) {
                toast({
                    title: 'Error',
                    description: 'Please select voice and locale',
                    variant: 'destructive',
                })
                return
            }
            // 获取当前用户的token
            const token = getToken()
            const headers: Record<string, string> = {}

            // 如果有token，添加到请求头
            if (token) {
                headers['Authorization'] = `Bearer ${token}`
            }

            const response = await axios.get('/api/text-to-speech', {
                responseType: 'blob',
                params: {
                    ...data.options,
                    service: data.service,
                    text: data.text
                },
                headers
            })
            const audioData = response.data

            const reader = new FileReader()
            const audioUri = await new Promise<string>((resolve) => {
                reader.onload = () => resolve(reader.result as string)
                reader.readAsDataURL(audioData)
            })
            await save({
                id: uuidv4(),
                createAt: Date.now(),
                text: data.text,
                service: data.service,
                options: data.options,
                uri: audioUri
            })
        } catch (error) {
            console.error('onSubmit error', error)
            toast({
                title: 'Error',
                description: 'Failed to generate audio',
                variant: 'destructive',
            })
        }
    })

    const legadoApiLink = useMemo(() => {
        const values = watchedFormValues
        if (!values || typeof window === 'undefined') return ''
        const protocol = window.location.protocol.replace(":", "")
        const host = window.location.host
        const params = new URLSearchParams()
        Object.entries(values.options).forEach(([key, value]) => {
            if (value != null && value !== '') params.set(key, String(value))
        })
        params.set('service', values.service)
        params.set('protocol', protocol)
        const token = getToken()
        if (token) params.set('token', token)
        return `${protocol}://${host}/api/legado-import?${params.toString()}`
    }, [watchedFormValues, getToken])

    const legadoImportLink = useMemo(() => {
        return `legado://import/httpTTS?src=${encodeURIComponent(legadoApiLink)}`
    }, [legadoApiLink])

    // 生成二维码
    const generateQrCode = async () => {
        if (!legadoApiLink) return

        try {
            const qrDataUrl = await QRCode.toDataURL(legadoApiLink, {
                width: 256,
                margin: 2,
                color: {
                    dark: '#000000',
                    light: '#FFFFFF'
                }
            })
            setQrCodeDataUrl(qrDataUrl)
            setShowQrDialog(true)
        } catch {
            toast({
                title: '错误',
                description: '生成二维码失败',
                variant: 'destructive',
            })
        }
    }

    return <div {...props}>
        <form
            className={clsx('flex flex-col-reverse size-full gap-4',
                'md:flex-row-reverse xl:flex-row'
            )}
            onSubmit={onSubmit}>
            <Form {...form}>
                <div className={clsx('flex flex-col justify-between gap-2',
                )}>
                    <div className={clsx(
                        'grid grid-cols-1 place-content-start gap-2',
                        'md:w-80 md:grid-cols-[repeat(1,minmax(10rem,1fr))]'
                    )}>
                        {/* 服务通道选择器 */}
                        <FormField name='service' control={form.control}
                            render={({ field }) => (
                                <FormItem>
                                    <Popover>
                                        <PopoverTrigger asChild>
                                            <Button
                                                variant="outline"
                                                role="combobox"
                                                className="justify-between w-full"
                                            >
                                                <span className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 w-16 truncate')}><Server className="h-4 w-4" />通道</span>
                                                <span className={clsx('ml-2 flex-1 text-left truncate')}>
                                                    {serviceOptions.find(s => s.value === field.value)?.label ?? "选择通道..."}
                                                </span>
                                                <ChevronsUpDown className="opacity-50" />
                                            </Button>
                                        </PopoverTrigger>
                                        <PopoverContent className="p-0 w-[--radix-popover-trigger-width]">
                                            <Command>
                                                <CommandList>
                                                    <CommandGroup>
                                                        {serviceOptions.map((option) => (
                                                            <PopoverPrimitive.Close asChild key={option.value}>
                                                                <div>
                                                                    <CommandItem
                                                                        value={option.value}
                                                                        onSelect={() => {
                                                                            field.onChange(option.value)
                                                                        }}
                                                                    >
                                                                        <div className="flex flex-col">
                                                                            <span>{option.label}</span>
                                                                            <span className="text-xs text-muted-foreground">{option.hint}</span>
                                                                        </div>
                                                                        <Check
                                                                            className={clsx(
                                                                                "ml-auto",
                                                                                field.value === option.value ? "opacity-100" : "opacity-0"
                                                                            )}
                                                                        />
                                                                    </CommandItem>
                                                                </div>
                                                            </PopoverPrimitive.Close>
                                                        ))}
                                                    </CommandGroup>
                                                </CommandList>
                                            </Command>
                                        </PopoverContent>
                                    </Popover>
                                    <FormMessage />
                                </FormItem>
                            )}
                        ></FormField>

                        {/* 地区选择器 */}
                        <div>
                            <Popover>
                                <PopoverTrigger asChild>
                                    <Button
                                        variant="outline"
                                        role="combobox"
                                        className="justify-between w-full"
                                    >
                                        <span className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 w-16 truncate')}><Globe className="h-4 w-4" />地区</span>
                                        <span className={clsx('ml-2 flex-1 text-left truncate')}>
                                            {selectedLocale ? getLocaleFriendlyName(selectedLocale) : "选择地区..."}
                                        </span>
                                        <ChevronsUpDown className="opacity-50" />
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent className="p-0 w-[--radix-popover-trigger-width] max-h-[--radix-popover-content-available-height]">
                                    <Command filter={(value, search, keywords = []) => {
                                        const extendValue = value + " " + keywords.join(" ") + getLocaleFriendlyName(value);
                                        if (extendValue.toLowerCase().includes(search.toLowerCase())) {
                                            return 1;
                                        }
                                        return 0;
                                    }}>
                                        <CommandInput placeholder="搜索地区..." />
                                        <CommandList>
                                            <CommandEmpty>{voicesQuery.isLoading ? <span className={clsx('flex px-2')}>
                                                <LoaderCircle className="h-4 w-4 animate-spin" />
                                                <span className={clsx('ml-2')}>加载中...</span>
                                            </span> : '未找到结果'}</CommandEmpty>
                                            <CommandGroup>
                                                <PopoverPrimitive.Close asChild>
                                                    <div>
                                                        <CommandItem
                                                            value="全部地区"
                                                            onSelect={() => {
                                                                setSelectedLocale('')
                                                            }}
                                                        >
                                                            全部地区
                                                            <Check
                                                                className={clsx(
                                                                    "ml-auto",
                                                                    selectedLocale === '' ? "opacity-100" : "opacity-0"
                                                                )}
                                                            />
                                                        </CommandItem>
                                                    </div>
                                                </PopoverPrimitive.Close>
                                                {availableLocales.map((locale) => (
                                                    <PopoverPrimitive.Close asChild key={locale}>
                                                        <div>
                                                            <CommandItem
                                                                value={locale}
                                                                onSelect={() => {
                                                                    setSelectedLocale(locale)
                                                                }}
                                                            >
                                                                {getLocaleFriendlyName(locale)}
                                                                <Check
                                                                    className={clsx(
                                                                        "ml-auto",
                                                                        selectedLocale === locale ? "opacity-100" : "opacity-0"
                                                                    )}
                                                                />
                                                            </CommandItem>
                                                        </div>
                                                    </PopoverPrimitive.Close>
                                                ))}
                                            </CommandGroup>
                                        </CommandList>
                                    </Command>
                                </PopoverContent>
                            </Popover>
                        </div>

                        {/* 发音人选择器 */}
                        <FormField name='options.voice' control={form.control}
                            render={({ field }) => (
                                <FormItem>
                                    <Popover>
                                        <PopoverTrigger asChild>
                                            <Button
                                                variant="outline"
                                                role="combobox"
                                                className="justify-between w-full"
                                            >
                                                <span className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 w-16 truncate')}><Speech className="h-4 w-4" />发音人</span>
                                                <span className={clsx('ml-2 flex-1 text-left truncate')}>
                                                    {field.value
                                                        ? getFriendlyVoiceName(field.value, currentVoice?.label ?? field.value)
                                                        : "选择发音人..."}
                                                </span>
                                                <ChevronsUpDown className="opacity-50" />
                                            </Button>
                                        </PopoverTrigger>
                                        <PopoverContent className="p-0 w-[--radix-popover-trigger-width] max-h-[--radix-popover-content-available-height]">
                                            <Command>
                                                <CommandInput placeholder="搜索发音人..." />
                                                <CommandList>
                                                    <CommandEmpty>{voicesQuery.isLoading ? <span className={clsx('flex px-2')}>
                                                        <LoaderCircle className="h-4 w-4 animate-spin" />
                                                        <span className={clsx('ml-2')}>加载中...</span>
                                                    </span> : '未找到结果'}</CommandEmpty>
                                                    <CommandGroup>
                                                        {currentLocaleVoices?.map((voice) => (
                                                            <PopoverPrimitive.Close asChild
                                                                key={voice.value}
                                                            >
                                                                <div>
                                                                    <CommandItem
                                                                        key={voice.value}
                                                                        value={voice.value}
                                                                        onSelect={(value: string) => {
                                                                            field.onChange(value)
                                                                        }}
                                                                    >
                                                                        {getFriendlyVoiceName(voice.value, voice.label)}
                                                                        <Check
                                                                            className={clsx(
                                                                                "ml-auto",
                                                                                field.value === voice.value ? "opacity-100" : "opacity-0"
                                                                            )}
                                                                        />
                                                                    </CommandItem>
                                                                </div>
                                                            </PopoverPrimitive.Close>
                                                        ))}
                                                    </CommandGroup>
                                                </CommandList>
                                            </Command>
                                        </PopoverContent>
                                    </Popover>
                                    <FormMessage />
                                </FormItem>
                            )}
                        ></FormField>
                        {selectedService === 'ms' && <>
                            <FormField name='options.safeMode' control={form.control}
                                render={({ field }) => (
                                    <FormItem className="rounded-md border p-3">
                                        <label className="flex items-center gap-2 text-sm">
                                            <input type="checkbox" checked={field.value ?? true} onChange={event => field.onChange(event.target.checked)} />
                                            <span>有声书稳定模式</span>
                                        </label>
                                        <p className="mt-1 text-xs text-muted-foreground">固定 24k/160k MP3；限制语速、音调和音量，避免异常发音。</p>
                                    </FormItem>
                                )}
                            />
                            <FormField name='options.omni' control={form.control}
                                render={({ field }) => (
                                    <FormItem className="rounded-md border p-3">
                                        <label className={clsx("flex items-center gap-2 text-sm", !currentVoice?.omniCapable && "opacity-50")}>
                                            <input type="checkbox" checked={Boolean(field.value)} disabled={!currentVoice?.omniCapable} onChange={event => field.onChange(event.target.checked)} />
                                            <span>全场景音色 (Omni)</span>
                                        </label>
                                        <p className="mt-1 text-xs text-muted-foreground">仅支持普通 Neural 音色；自动使用 Dragon HD Omni 并避免不稳定格式。</p>
                                    </FormItem>
                                )}
                            />
                            <FormField name='options.region' control={form.control}
                                render={({ field }) => (
                                    <FormItem>
                                        <Label>微软 TTS 区域</Label>
                                        <select className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={field.value || 'auto'} onChange={field.onChange}>
                                            {msRegions.map(region => <option key={region.value} value={region.value}>{region.label}</option>)}
                                        </select>
                                    </FormItem>
                                )}
                            />
                            <FormField name='options.format' control={form.control}
                                render={({ field }) => (
                                    <FormItem>
                                        <Label>输出格式</Label>
                                        <select className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={field.value || 'audio-24khz-160kbitrate-mono-mp3'} onChange={field.onChange} disabled={selectedSafeMode}>
                                            {msOutputFormats.map(format => <option key={format} value={format}>{formatLabel(format)}</option>)}
                                        </select>
                                        {selectedSafeMode && <p className="text-xs text-muted-foreground">稳定模式固定为 24kHz / 160k MP3。</p>}
                                    </FormItem>
                                )}
                            />
                            {languageSkillChoices.length > 0 && <FormField name='options.langSkill' control={form.control}
                                render={({ field }) => (
                                    <FormItem>
                                        <Label>语言技能 (langSkill)</Label>
                                        <select className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={field.value || ''} onChange={event => field.onChange(event.target.value || undefined)}>
                                            <option value="">默认音色语言</option>
                                            {languageSkillChoices.map(item => <option key={item.value} value={item.value}>{item.label} ({item.value})</option>)}
                                        </select>
                                    </FormItem>
                                )}
                            />}
                        </>}

                        {(selectedService !== 'ms' || (!selectedSafeMode && !selectedOmni)) && <FormField name='options.personality' control={form.control}
                            render={({ field }) => (
                                <FormItem>
                                    <Popover>
                                        <PopoverTrigger asChild>
                                            <Button
                                                variant="outline"
                                                role="combobox"
                                                className="justify-between w-full"
                                            >
                                                <span className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 w-16 truncate')}><Smile className="h-4 w-4" />风格</span>
                                                <span className={clsx('ml-2 flex-1 text-left truncate')}>
                                                    {field.value
                                                        ? personalityLabel(field.value, selectedService)
                                                        : "默认"}
                                                </span>
                                                <ChevronsUpDown className="opacity-50" />
                                            </Button>
                                        </PopoverTrigger>
                                        <PopoverContent className="p-0 w-[--radix-popover-trigger-width] max-h-[--radix-popover-content-available-height]">
                                            <Command>
                                                <CommandInput placeholder="Search voice..." />
                                                <CommandList>
                                                    <CommandEmpty>{voicesQuery.isLoading ? <span className={clsx('flex px-2')}>
                                                        <LoaderCircle className="h-4 w-4 animate-spin" />
                                                        <span className={clsx('ml-2')}>Loading...</span>
                                                    </span> : 'No results found.'}</CommandEmpty>
                                                    <CommandGroup>
                                                        <PopoverPrimitive.Close asChild

                                                        >
                                                            <div>
                                                                <CommandItem
                                                                    value={undefined as unknown as string}
                                                                    onSelect={() => {
                                                                        field.onChange(undefined)
                                                                    }}
                                                                >
                                                                    默认
                                                                    <Check
                                                                        className={clsx(
                                                                            "ml-auto",
                                                                            field.value === undefined ? "opacity-100" : "opacity-0"
                                                                        )}
                                                                    />

                                                                </CommandItem>
                                                            </div>
                                                        </PopoverPrimitive.Close>
                                                        {selectedService === 'ms' && !selectedSafeMode && <PopoverPrimitive.Close asChild>
                                                            <div><CommandItem value="auto" onSelect={() => field.onChange('auto')}>
                                                                自动风格（按规则/情绪桥接）
                                                                <Check className={clsx("ml-auto", field.value === 'auto' ? "opacity-100" : "opacity-0")} />
                                                            </CommandItem></div>
                                                        </PopoverPrimitive.Close>}
                                                        {currentVoice?.personalities.map((personality) => (
                                                            <PopoverPrimitive.Close asChild
                                                                key={personality}
                                                            >
                                                                <div>
                                                                    <CommandItem
                                                                        value={personality}
                                                                        onSelect={(value: string) => {
                                                                            field.onChange(value)
                                                                        }}
                                                                    >
                                                                        {personalityLabel(personality, selectedService)}
                                                                        <Check
                                                                            className={clsx(
                                                                                "ml-auto",
                                                                                field.value === personality ? "opacity-100" : "opacity-0"
                                                                            )}
                                                                        />
                                                                    </CommandItem>
                                                                </div>
                                                            </PopoverPrimitive.Close>
                                                        ))}
                                                    </CommandGroup>
                                                </CommandList>
                                            </Command>
                                        </PopoverContent>
                                    </Popover>
                                    <FormMessage />
                                </FormItem>
                            )}
                        ></FormField>}
                        {selectedService === 'ms' && !selectedSafeMode && selectedPersonality === 'auto' && <FormField name='options.autoStyleRules' control={form.control}
                            render={({ field }) => (
                                <FormItem>
                                    <Label>自动风格规则</Label>
                                    <Textarea className="min-h-20" placeholder={'cheerful[开心|哈哈]\nsad[难过|悲伤]\nangry[愤怒|滚]'} value={field.value ?? ''} onChange={field.onChange} />
                                    <p className="text-xs text-muted-foreground">每行格式：style[关键词|关键词]；匹配正文后自动选择对应风格。</p>
                                </FormItem>
                            )}
                        />}
                        {selectedService === 'ms' && !selectedSafeMode && !selectedOmni && (currentVoice?.roles?.length ?? 0) > 0 && <FormField name='options.role' control={form.control}
                            render={({ field }) => (
                                <FormItem>
                                    <Label>角色 (role)</Label>
                                    <select className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={field.value || 'default'} onChange={event => field.onChange(event.target.value === 'default' ? undefined : event.target.value)}>
                                        <option value="default">默认角色</option>
                                        {currentVoice?.roles?.map(role => <option key={role} value={role}>{role}</option>)}
                                    </select>
                                </FormItem>
                            )}
                        />}
                        {selectedService === 'ms' && !selectedSafeMode && !selectedOmni && <FormField name='options.styleDegree' control={form.control}
                            render={({ field }) => (
                                <FormItem>
                                    <Label>风格强度 (styleDegree)：{(field.value ?? 1).toFixed(1)}</Label>
                                    <input className="w-full" type="range" min="0" max="2" step="0.1" value={field.value ?? 1} onChange={event => field.onChange(Number(event.target.value))} />
                                </FormItem>
                            )}
                        />}
                        <div className={clsx('space-y-1')}>
                            <Label className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 w-16 truncate')}>语速</Label>
                            <FormField name="options.rate" control={form.control} render={({ field }) => (
                                <FormItem>
                                    <Slider
                                        value={[field.value ?? 0]}
                                        onValueChange={(value) => field.onChange(value[0])}
                                        min={-100}
                                        max={100}
                                        step={1}
                                    />
                                </FormItem>
                            )} />
                        </div>
                        <div className={clsx('space-y-1')}>
                            <Label className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 w-16 truncate')}>语调</Label>
                            <FormField name="options.pitch" control={form.control} render={({ field }) => (
                                <FormItem>
                                    <Slider
                                        value={[field.value ?? 0]}
                                        onValueChange={(value) => field.onChange(value[0])}
                                        min={-100}
                                        max={100}
                                        step={1}
                                    />
                                </FormItem>
                            )} />
                        </div>
                        <div className={clsx('space-y-1')}>
                            <Label className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 w-16 truncate')}>音量</Label>
                            <FormField name="options.volume" control={form.control} render={({ field }) => (
                                <FormItem>
                                    <Slider
                                        value={[field.value ?? 0]}
                                        onValueChange={(value) => field.onChange(value[0])}
                                        min={-100}
                                        max={100}
                                        step={1}
                                    />
                                </FormItem>
                            )} />
                        </div>
                        <div className={clsx('space-y-1')}></div>
                        <div className={clsx('space-y-3')}>
                            <Button type="submit" className={clsx('w-full')} disabled={form.formState.isSubmitting}>
                                {form.formState.isSubmitting ? (
                                    <span className={clsx('flex justify-between items-center gap-1 text-sm opacity-50 truncate')}>
                                        <LoaderCircle className="h-4 w-4 animate-spin" />
                                        <span className={clsx('ml-2')}>转换中...</span>
                                    </span>
                                ) : (
                                    '转换'
                                )}
                            </Button>

                            {/* 导入到阅读APP区域 */}
                            <div className="border rounded-lg p-4 space-y-3">
                                <h3 className="text-sm font-medium text-muted-foreground">导入到阅读APP</h3>
                                <div className="flex gap-2">
                                    <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        className="flex-1"
                                        disabled={!form.getValues('options.voice')}
                                        onClick={() => {
                                            // Open import link in new window
                                            window.open(legadoImportLink)
                                        }}
                                    >
                                        <Copy className="h-4 w-4" />
                                        直接导入
                                    </Button>
                                    <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        className="flex-1"
                                        disabled={!form.getValues('options.voice')}
                                        onClick={async () => {
                                            await generateQrCode()
                                            setShowQrDialog(true)
                                        }}
                                    >
                                        <QrCode className="h-4 w-4" />
                                        扫码导入
                                    </Button>
                                </div>
                                <p className="text-xs text-muted-foreground">
                                    选择发音人后可点击按钮或扫描二维码导入TTS配置到阅读APP
                                </p>
                            </div>
                        </div>
                    </div>
                </div>
            </Form>

            <div className={clsx('flex-1 flex flex-col gap-2')}>
                <Textarea className={clsx('flex-1 resize-none h-full')} placeholder={'请输入内容'}
                    {...form.register('text')}
                />
            </div>
        </form>

        {/* 二维码对话框 */}
        <Dialog open={showQrDialog} onOpenChange={setShowQrDialog}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>扫描二维码导入到阅读APP</DialogTitle>
                </DialogHeader>
                <div className="flex flex-col items-center space-y-4">
                    {qrCodeDataUrl && (
                        <Image
                            src={qrCodeDataUrl}
                            alt="QR Code"
                            width={256}
                            height={256}
                            className="w-64 h-64"
                        />
                    )}
                    <p className="text-sm text-muted-foreground">{legadoApiLink}</p>
                    <p className="text-sm text-muted-foreground text-center">
                        使用浏览器扫描此二维码后复制内容导入TTS配置
                    </p>
                </div>
            </DialogContent>
        </Dialog>
    </div>
}