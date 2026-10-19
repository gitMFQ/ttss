import { concatArrayBuffers } from '../utils'
import axios from 'axios'
import { WebSocket } from 'ws'
import { generateSecMsGecV2 } from './drm'
import { TRUSTED_CLIENT_TOKEN, CHROMIUM_FULL_VERSION, CHROMIUM_MAJOR_VERSION } from './drm-constants'

export { CHROMIUM_FULL_VERSION, TRUSTED_CLIENT_TOKEN }

class MessageHeader {
    requestId: string
    contentType?: string
    path: string
    streamId?: string
    constructor(requestId: string, path: string, contentType?: string, streamId?: string) {
        this.requestId = requestId
        this.contentType = contentType
        this.path = path
        this.streamId = streamId
    }
    public static parse(data: string): MessageHeader {
        const requestIdPattern = /X-RequestId:(?<id>[a-z|0-9]*)/
        const contentTypePattern = /Content-Type:(?<type>[^\r\n]*)/
        const pathPattern = /Path:(?<path>[^\r\n]+)/
        const streamIdPattern = /X-StreamId:(?<id>[^\r\n]*)/
        const requestIdMatch = requestIdPattern.exec(data)
        if (requestIdMatch === null) {
            throw new Error('RequestId not found: \n' + data,)
        }
        const contentTypeMatch = contentTypePattern.exec(data)
        const pathMatch = pathPattern.exec(data)
        if (pathMatch === null) {
            throw new Error('Path not found: \n' + data)
        }
        const streamIdMatch = streamIdPattern.exec(data)
        return new MessageHeader(requestIdMatch.groups!.id, pathMatch.groups!.path, contentTypeMatch?.groups?.type, streamIdMatch?.groups?.id)
    }

    public toString(): string {
        let header = `X-RequestId:${this.requestId}\r\n`
        header += `Content-Type:${this.contentType}; charset=UTF-8\r\n`
        if (this.streamId) {
            header += `X-StreamId:${this.streamId}\r\n`
        }
        header += `Path:${this.path}\r\n`
        return header
    }
}

function createConfigMessage(options: ClientOptions) {
    return {
        context: {
            synthesis: {
                audio: {
                    metadataoptions: {
                        sentenceBoundaryEnabled: options.sentenceBoundaryEnabled ? 'true' : 'false',
                        wordBoundaryEnabled: options.wordBoundaryEnabled ? 'true' : 'false',
                    },
                    outputFormat: options.format,
                },
            },
        },
    }
}

export interface ClientOptions {
    format: string
    sentenceBoundaryEnabled?: boolean
    wordBoundaryEnabled?: boolean
}

export interface VoiceMetadata {
    [key: string]: unknown
}

export interface ConvertResult {
    audio: ArrayBuffer
    metadata: VoiceMetadata[]
}

// ---------------------------------------------------------------------------
// Proxy + pool configuration
// ---------------------------------------------------------------------------

// Read once at module load so behaviour is stable across requests.
function readProxyUrl(): string | null {
    const v = process.env.TTS_PROXY || process.env.HTTPS_PROXY || process.env.HTTP_PROXY
    if (!v) return null
    const trimmed = v.trim()
    return trimmed.length > 0 ? trimmed : null
}

interface PoolConfig {
    proxyUrl: string | null
    poolSize: number
    idleTimeoutMs: number
    perRequestTimeoutMs: number
    pingIntervalMs: number
}

function loadPoolConfig(): PoolConfig {
    const poolSize = (() => {
        const raw = process.env.TTS_POOL_SIZE
        if (!raw) return 3
        const n = parseInt(raw, 10)
        return Number.isFinite(n) && n >= 1 && n <= 32 ? n : 3
    })()
    const idleMinutes = (() => {
        const raw = process.env.TTS_IDLE_TIMEOUT_MIN
        if (!raw) return 5
        const n = parseFloat(raw)
        return Number.isFinite(n) && n >= 0.5 ? n : 5
    })()
    return {
        proxyUrl: readProxyUrl(),
        poolSize,
        idleTimeoutMs: Math.floor(idleMinutes * 60_000),
        perRequestTimeoutMs: 60_000,
        pingIntervalMs: 30_000,
    }
}

// Build the agent lazily so importing this module from a context where
// `process` isn't defined (rare) still works for tests.
async function buildAgent(proxyUrl: string | null): Promise<any | undefined> {
    if (!proxyUrl) return undefined
    try {
        // Dynamic import keeps startup fast for users who don't use a proxy.
        const mod: any = await import('https-proxy-agent')
        // 7.x exports { HttpsProxyAgent }; 5.x exposes it as module.exports.
        const Ctor = mod.HttpsProxyAgent ?? mod.default?.HttpsProxyAgent ?? mod.default
        if (typeof Ctor !== 'function') {
            console.warn('[EdgeTTSClient] https-proxy-agent missing HttpsProxyAgent export; ignoring proxy')
            return undefined
        }
        return new Ctor(proxyUrl)
    } catch (e) {
        console.warn('[EdgeTTSClient] failed to load https-proxy-agent, ignoring proxy:', (e as Error).message)
        return undefined
    }
}

// ---------------------------------------------------------------------------
// Connection pool: each pool slot owns one WebSocket and a FIFO queue.
// ---------------------------------------------------------------------------

type PendingTask = {
    requestId: string
    ssml: string
    options: ClientOptions
    resolve: (r: ConvertResult) => void
    reject: (e: Error) => void
    startedAt: number
    timer: NodeJS.Timeout
    audio: ArrayBuffer
    metadata: VoiceMetadata[]
    settled: boolean
}

class ConnectionSlot {
    public ws: WebSocket | null = null
    public ready: Promise<void> | null = null
    public queue: PendingTask[] = []
    public busy = false
    public lastUsedAt = 0
    public idleTimer: NodeJS.Timeout | null = null
    public pingTimer: NodeJS.Timeout | null = null
    public alive = false
    public deadReason: string | null = null

    constructor(public readonly index: number, private readonly cfg: PoolConfig, private readonly agent: any) {}

    private generateId(): string {
        const charset = 'abcdef0123456789'
        let randomString = ''
        for (let i = 0; i < 32; i++) {
            const randomIndex = Math.floor(Math.random() * charset.length)
            randomString += charset.charAt(randomIndex)
        }
        return randomString
    }

    private async buildWebSocket(): Promise<WebSocket> {
        const connectionId = this.generateId()
        const secMsGec = generateSecMsGecV2(Date.now() / 1000)
        const url = `wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1?TrustedClientToken=${TRUSTED_CLIENT_TOKEN}&Sec-MS-GEC=${secMsGec}&Sec-MS-GEC-Version=2-${CHROMIUM_FULL_VERSION}&ConnectionId=${connectionId}`
        const opts: any = {
            host: 'speech.platform.bing.com',
            origin: 'chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold',
            headers: {
                'User-Agent': `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${CHROMIUM_MAJOR_VERSION}.0.0.0 Safari/537.36 Edg/${CHROMIUM_MAJOR_VERSION}.0.0.0`,
            },
        }
        if (this.agent) opts.agent = this.agent
        const ws = new WebSocket(url, opts)
        // Register message handler immediately, before any await, so we don't
        // miss frames that arrive between open and ensureReady attaching.
        ws.on('message', (data: any, isBinary: boolean) => this.handleMessage(data, isBinary))
        return new Promise<WebSocket>((resolve, reject) => {
            const onError = (err: Error) => {
                ws.removeListener('open', onOpen)
                reject(err)
            }
            const onOpen = () => {
                ws.removeListener('error', onError)
                resolve(ws)
            }
            ws.once('open', onOpen)
            ws.once('error', onError)
        })
    }

    /** Start (or restart) this slot's WebSocket. */
    private async ensureReady(): Promise<void> {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) return
        if (this.ready) return this.ready
        this.deadReason = null
        this.ready = (async () => {
            const ws = await this.buildWebSocket()
            this.ws = ws
            this.alive = true
            this.lastUsedAt = Date.now()

            ws.on('close', (code: number, reason: Buffer) => {
                this.alive = false
                if (this.deadReason == null) this.deadReason = `closed code=${code} reason=${reason?.toString() ?? ''}`
                // Fail any pending tasks.
                while (this.queue.length > 0) {
                    const task = this.queue.shift()!
                    clearTimeout(task.timer)
                    if (!task.settled) {
                        task.settled = true
                        task.reject(new Error(`WebSocket closed (${this.deadReason}) before turn.end`))
                    }
                }
                this.busy = false
                this.cleanupTimers()
                this.ws = null
                this.ready = null
            })

            ws.on('error', (e: Error) => {
                this.alive = false
                if (this.deadReason == null) this.deadReason = `error: ${e.message}`
            })

            // Health check: ping every 30s.
            this.pingTimer = setInterval(() => {
                if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
                try { this.ws.ping() } catch { /* ignore */ }
            }, this.cfg.pingIntervalMs)

            // Process the queue as soon as we're open.
            this.drainQueue()
        })().catch((e) => {
            this.ready = null
            this.alive = false
            this.deadReason = (e as Error).message
            throw e
        })
        return this.ready
    }

    private handleMessage(data: any, isBinary: boolean) {
        // ws v8 hands text frames over as Buffer too, so trust its isBinary
        // flag rather than guessing from the leading bytes.
        if (isBinary || (data instanceof Buffer && !this.looksLikeTextFrame(data))) {
            if (data instanceof Buffer) {
                this.handleBinaryFrame(data)
            }
            return
        }
        if (data instanceof Buffer) {
            this.handleTextFrame(data.toString('utf-8'))
        } else if (typeof data === 'string') {
            this.handleTextFrame(data)
        }
    }

    /** A text frame starts with ASCII header text such as "X-RequestId:". */
    private looksLikeTextFrame(data: Buffer): boolean {
        return data.length > 0 && data[0] === 0x58 /* 'X' */
    }

    /**
     * Binary frame layout: uint16 big-endian header length, header text, then
     * the audio payload. The header length counts the header bytes only, so the
     * payload starts at 2 + headerLength.
     */
    private handleBinaryFrame(data: Buffer) {
        if (data.length < 2) return
        const headerLength = data.readUInt16BE(0)
        if (headerLength <= 0 || data.length < 2 + headerLength) return
        const headerString = data.subarray(2, 2 + headerLength).toString('utf-8')
        let header: MessageHeader
        try {
            header = MessageHeader.parse(headerString)
        } catch {
            return
        }
        if (header.path !== 'audio') return
        const task = this.findTask(header.requestId)
        if (!task) return
        const payload = data.subarray(2 + headerLength)
        if (payload.length === 0) return
        const copy = new Uint8Array(payload.byteLength)
        copy.set(payload)
        task.audio = concatArrayBuffers(task.audio, copy.buffer)
    }

    private handleTextFrame(text: string) {
        const spliter = '\r\n\r\n'
        const headerStringEnd = text.indexOf(spliter) + spliter.length
        if (headerStringEnd <= spliter.length) return
        const headerString = text.slice(0, headerStringEnd)
        let header: MessageHeader
        try {
            header = MessageHeader.parse(headerString)
        } catch {
            return
        }
        const body = text.slice(headerStringEnd)
        switch (header.path) {
            case 'turn.start':
                break
            case 'audio.metadata': {
                try {
                    const parsed = JSON.parse(body)
                    if (parsed && parsed.Metadata instanceof Array) {
                        const task = this.findTask(header.requestId)
                        if (task) task.metadata.push(...parsed.Metadata)
                    }
                } catch { /* ignore */ }
                break
            }
            case 'turn.end': {
                const task = this.findTask(header.requestId)
                if (task) this.completeTask(task, { audio: task.audio, metadata: task.metadata })
                break
            }
        }
    }

    private findTask(requestId: string): PendingTask | null {
        if (this.queue.length === 0) return null
        if (this.queue[0].requestId === requestId) return this.queue[0]
        // Defensive: shouldn't happen if the queue is strictly FIFO, but match anyway.
        return this.queue.find((t) => t.requestId === requestId) ?? null
    }

    private completeTask(task: PendingTask, result: ConvertResult) {
        if (task.settled) return
        task.settled = true
        clearTimeout(task.timer)
        const idx = this.queue.indexOf(task)
        if (idx >= 0) this.queue.splice(idx, 1)
        task.resolve(result)
        this.lastUsedAt = Date.now()
        // Only the head owns the busy flag; removing a queued task must not
        // clear it or the next drain would double-send on this socket.
        if (idx === 0) {
            this.busy = false
            this.drainQueue()
        }
        this.scheduleIdleClose()
    }

    private failTask(task: PendingTask, err: Error) {
        if (task.settled) return
        task.settled = true
        clearTimeout(task.timer)
        const idx = this.queue.indexOf(task)
        if (idx >= 0) this.queue.splice(idx, 1)
        task.reject(err)
        if (idx === 0) {
            this.busy = false
            this.drainQueue()
        }
        this.scheduleIdleClose()
    }

    private scheduleIdleClose() {
        if (this.cfg.idleTimeoutMs <= 0) return
        if (this.idleTimer) clearTimeout(this.idleTimer)
        this.idleTimer = setTimeout(() => {
            // Only close if no pending work.
            if (this.queue.length === 0 && this.ws && this.ws.readyState === WebSocket.OPEN) {
                this.alive = false
                this.cleanupTimers()
                try { this.ws.close(1000, 'idle timeout') } catch { /* ignore */ }
            }
        }, this.cfg.idleTimeoutMs)
    }

    private cleanupTimers() {
        if (this.idleTimer) { clearTimeout(this.idleTimer); this.idleTimer = null }
        if (this.pingTimer) { clearInterval(this.pingTimer); this.pingTimer = null }
    }

    private async drainQueue() {
        if (this.busy) return
        while (this.queue.length > 0 && this.ws && this.ws.readyState === WebSocket.OPEN) {
            const task = this.queue[0]
            this.busy = true
            try {
                const config = createConfigMessage(task.options)
                const configMessage =
                    `X-Timestamp:${new Date()}\r\n` +
                    'Content-Type:application/json; charset=utf-8\r\n' +
                    'Path:speech.config\r\n\r\n' +
                    JSON.stringify(config)
                const ssmlMessage =
                    `X-Timestamp:${new Date()}\r\n` +
                    `X-RequestId:${task.requestId}\r\n` +
                    'Content-Type:application/ssml+xml\r\n' +
                    `Path:ssml\r\n\r\n` +
                    task.ssml
                await new Promise<void>((resolve, reject) => {
                    this.ws!.send(configMessage, (err) => err ? reject(err) : resolve())
                })
                await new Promise<void>((resolve, reject) => {
                    this.ws!.send(ssmlMessage, (err) => err ? reject(err) : resolve())
                })
                task.startedAt = Date.now()
                // IMPORTANT: we just submitted a turn. We must wait for turn.end
                // (handled by handleMessage → completeTask) before draining the
                // next task. Break out of the loop; completeTask will re-drain.
                return
            } catch (e) {
                this.failTask(task, e as Error)
            }
        }
    }

    public submit(ssml: string, options: ClientOptions, timeoutMs: number): Promise<ConvertResult> {
        const requestId = (() => {
            const charset = 'abcdef0123456789'
            let s = ''
            for (let i = 0; i < 32; i++) s += charset.charAt(Math.floor(Math.random() * charset.length))
            return s
        })()
        return new Promise<ConvertResult>((resolve, reject) => {
            const task: PendingTask = {
                requestId,
                ssml,
                options,
                resolve,
                reject,
                startedAt: Date.now(),
                audio: new ArrayBuffer(0),
                metadata: [],
                settled: false,
                timer: setTimeout(() => {
                    if (task.settled) return
                    task.settled = true
                    const idx = this.queue.indexOf(task)
                    if (idx >= 0) this.queue.splice(idx, 1)
                    this.scheduleIdleClose()
                    reject(new Error(`Edge TTS request timed out after ${timeoutMs}ms`))
                    // A stalled head turn leaves the socket in an unknown state,
                    // so drop it; a timed-out queued turn does not.
                    if (idx === 0) {
                        this.busy = false
                        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                            try { this.ws.close(1000, 'request timeout') } catch { /* ignore */ }
                        }
                    } else {
                        this.drainQueue()
                    }
                }, timeoutMs),
            }
            this.queue.push(task)
            this.ensureReady().then(() => this.drainQueue()).catch((e) => {
                this.failTask(task, e as Error)
            })
        })
    }

    public shutdown() {
        this.cleanupTimers()
        const ws = this.ws
        this.ws = null
        this.alive = false
        this.busy = false
        if (ws) {
            try { ws.close(1000, 'shutdown') } catch { /* ignore */ }
        }
        while (this.queue.length > 0) {
            const task = this.queue.shift()!
            clearTimeout(task.timer)
            if (!task.settled) {
                task.settled = true
                task.reject(new Error('Edge TTS pool shutting down'))
            }
        }
    }
}

class EdgeTTSPool {
    private static instance: EdgeTTSPool | null = null
    private slots: ConnectionSlot[] = []
    private cfg: PoolConfig
    private agent: any = undefined
    private agentReady: Promise<void> | null = null
    private roundRobinIdx = 0
    private initializing: Promise<void> | null = null

    private constructor() {
        this.cfg = loadPoolConfig()
        this.agentReady = buildAgent(this.cfg.proxyUrl).then((a) => { this.agent = a })
        if (this.cfg.proxyUrl) {
            console.log(`[EdgeTTSClient] proxy=${this.cfg.proxyUrl}, poolSize=${this.cfg.poolSize}, idleTimeoutMs=${this.cfg.idleTimeoutMs}`)
        } else {
            console.log(`[EdgeTTSClient] direct (no proxy), poolSize=${this.cfg.poolSize}, idleTimeoutMs=${this.cfg.idleTimeoutMs}`)
        }
    }

    static get(): EdgeTTSPool {
        if (!EdgeTTSPool.instance) {
            EdgeTTSPool.instance = new EdgeTTSPool()
        }
        return EdgeTTSPool.instance
    }

    /** For tests / hot reload: tear down everything. */
    static reset() {
        if (EdgeTTSPool.instance) {
            EdgeTTSPool.instance.shutdown()
            EdgeTTSPool.instance = null
        }
    }

    private async ensureSlots(): Promise<void> {
        if (this.agentReady) await this.agentReady
        if (this.initializing) return this.initializing
        this.initializing = (async () => {
            for (let i = 0; i < this.cfg.poolSize; i++) {
                this.slots.push(new ConnectionSlot(i, this.cfg, this.agent))
            }
        })().catch((e) => {
            this.initializing = null
            throw e
        })
        return this.initializing
    }

    /**
     * Pick the least-loaded live slot. When every slot is dead we round-robin
     * so concurrent callers rebuild different sockets instead of stampeding
     * the same one.
     */
    private pickSlot(): ConnectionSlot {
        const alive = this.slots.filter((s) => s.alive)
        if (alive.length === 0) {
            const slot = this.slots[this.roundRobinIdx % this.slots.length]
            this.roundRobinIdx = (this.roundRobinIdx + 1) % this.slots.length
            return slot
        }
        let best = alive[0]
        for (const slot of alive.slice(1)) {
            if (slot.queue.length < best.queue.length) best = slot
        }
        return best
    }

    async convert(ssml: string, options: ClientOptions): Promise<ConvertResult> {
        await this.ensureSlots()
        const slot = this.pickSlot()
        return slot.submit(ssml, options, this.cfg.perRequestTimeoutMs)
    }

    shutdown() {
        for (const s of this.slots) s.shutdown()
        this.slots = []
    }
}

// ---------------------------------------------------------------------------
// Public API (unchanged shape) — now backed by the pool.
// ---------------------------------------------------------------------------

export class EdgeTTSClient {
    private constructor() {
    }
    public static async voices(): Promise<unknown> {
        const url = 'https://speech.platform.bing.com/consumer/speech/synthesize/readaloud/voices/list?trustedclienttoken=6A5AA1D4EAFF4E9FB37E23D68491D6F4'
        const response = await axios.get(url)
        const data = response.data
        return data
    }

    public static async convert(ssml: string, options: ClientOptions): Promise<ConvertResult> {
        console.log('[EdgeTTSClient] convert (pooled)', { format: options.format, ssmlLen: ssml.length })
        return EdgeTTSPool.get().convert(ssml, options)
    }
}

// On process shutdown, gracefully close the pool. Guarded so dev-mode HMR
// re-evaluating this module does not stack handlers on the same process.
const SHUTDOWN_FLAG = Symbol.for('ms-tts.edge.shutdown-hooked')
const globalState = globalThis as unknown as Record<symbol, boolean>
if (!globalState[SHUTDOWN_FLAG]) {
    globalState[SHUTDOWN_FLAG] = true
    const shutdownHandler = () => {
        EdgeTTSPool.reset()
    }
    process.once('SIGINT', shutdownHandler)
    process.once('SIGTERM', shutdownHandler)
}
