/**
 * Sec-MS-GEC v2 token generation.
 *
 * Ported from edge-tts (rany2/edge-tts) src/edge_tts/drm.py.
 * Microsoft replaced the v1 plain SHA-256 token with a v2 token that combines a
 * modified SHA-256 digest with a custom CS64 digest, formatted as
 * "<16 hex>-<64 hex>". Servers reject v1 tokens with HTTP 403 on handshake.
 *
 * Note: the digest here is NOT standard SHA-256. Upstream deliberately perturbs
 * the schedule and round constants, so node:crypto's sha256 cannot be used.
 *
 * All 32-bit words are held as BigInt. JS number cannot hold the 64-bit
 * intermediates (e.g. word * 0x100000001) without losing precision.
 */
import { TRUSTED_CLIENT_TOKEN } from './drm-constants'
import {
  SHA256_ROUND_CONSTANTS,
  SHA256_SCHEDULE,
  SHA256_ADJUSTMENT,
  SHA256_SIGMA0_ADJUSTMENT,
  SHA256_SIGMA1_ADJUSTMENT,
  CS64_COEFFICIENTS,
  CS64_FACTOR,
  CS64_ADJUSTMENT0,
  CS64_ADJUSTMENT1,
  SHA256_INITIAL,
  SHA256_STATE,
} from './drm-constants'

const MASK = 0xffffffffn
const U32 = 0x100000000n

/** Seconds offset between the Unix and Windows file-time epochs. */
const WINDOWS_FILE_TIME_EPOCH = 11644473600

function u32(v: bigint): bigint {
  return v & MASK
}

/** Python's % is non-negative; JS's keeps the sign of the dividend. */
function euclideanMod(a: number, b: number): number {
  const r = a % b
  return r < 0 ? r + b : r
}

function rol16(value: bigint): bigint {
  const v = u32(value)
  return u32((v << 16n) | (v >> 16n))
}

/** Split data into little-endian 32-bit words, zero-padded to an even count. */
function wordsLE4Even(data: Buffer): bigint[] {
  const padded = Buffer.from(data)
  const rem = padded.length % 4
  const buf = rem === 0 ? padded : Buffer.concat([padded, Buffer.alloc(4 - rem)])
  if (buf.length === 0) return []
  const words: bigint[] = []
  for (let i = 0; i < buf.length; i += 4) {
    words.push(BigInt(buf.readUInt32LE(i)))
  }
  if (words.length % 2 !== 0) words.push(0n)
  return words
}

function sigma0(value: bigint): bigint {
  const v = u32(value)
  return u32((v >> 7n) ^ (v << 25n) ^ (v >> 18n) ^ (v << 14n) ^ (v >> 3n))
}

function sigma1(value: bigint): bigint {
  const v = u32(value)
  return u32((v >> 17n) ^ (v << 15n) ^ (v >> 19n) ^ (v << 13n) ^ (v >> 10n))
}

/** ROTR over 6/11/25 by duplicating the word in 64 bits, as upstream does. */
function rotr6_11_25(e: bigint): bigint {
  const v = u32(e) * (U32 + 1n)
  return (v >> 6n) ^ (v >> 11n) ^ (v >> 25n)
}

function rotr2_13_22(a: bigint): bigint {
  const v = u32(a) * (U32 + 1n)
  return (v >> 2n) ^ (v >> 13n) ^ (v >> 22n)
}

type ShaState = bigint[]

function sha256Rounds(state: ShaState, words: bigint[], firstRound: number): ShaState {
  let [a, b, c, d, e, f, g, h] = state
  const constants =
    firstRound === 14
      ? SHA256_ROUND_CONSTANTS.map((v, i) => (i === 14 ? SHA256_ADJUSTMENT : v))
      : SHA256_ROUND_CONSTANTS

  const aligned = Math.min(64, Math.floor((firstRound + 7) / 8) * 8)
  for (let index = firstRound; index < aligned; index++) {
    const t1 = u32(h + rotr6_11_25(e) + (g ^ (e & (f ^ g))) + constants[index] + words[index])
    const t2 = u32(rotr2_13_22(a) + ((a & b) | (c & (a | b))))
    // Python assigns the whole tuple at once, so e_new must use the OLD d.
    ;[a, b, c, d, e, f, g, h] = [u32(t1 + t2), a, b, c, u32(d + t1), e, f, g]
  }
  for (let index = aligned; index < 64; index += 8) {
    let t1 = u32(h + rotr6_11_25(e) + (g ^ (e & (f ^ g))) + constants[index] + words[index])
    d = u32(d + t1)
    h = u32(t1 + rotr2_13_22(a) + ((a & b) | (c & (a | b))))

    t1 = u32(g + rotr6_11_25(d) + (f ^ (d & (e ^ f))) + constants[index + 1] + words[index + 1])
    c = u32(c + t1)
    g = u32(t1 + rotr2_13_22(h) + ((h & a) | (b & (h | a))))

    t1 = u32(f + rotr6_11_25(c) + (e ^ (c & (d ^ e))) + constants[index + 2] + words[index + 2])
    b = u32(b + t1)
    f = u32(t1 + rotr2_13_22(g) + ((g & h) | (a & (g | h))))

    t1 = u32(e + rotr6_11_25(b) + (d ^ (b & (c ^ d))) + constants[index + 3] + words[index + 3])
    a = u32(a + t1)
    e = u32(t1 + rotr2_13_22(f) + ((f & g) | (h & (f | g))))

    t1 = u32(d + rotr6_11_25(a) + (c ^ (a & (b ^ c))) + constants[index + 4] + words[index + 4])
    h = u32(h + t1)
    d = u32(t1 + rotr2_13_22(e) + ((e & f) | (g & (e | f))))

    t1 = u32(c + rotr6_11_25(h) + (b ^ (h & (a ^ b))) + constants[index + 5] + words[index + 5])
    g = u32(g + t1)
    c = u32(t1 + rotr2_13_22(d) + ((d & e) | (f & (d | e))))

    t1 = u32(b + rotr6_11_25(g) + (a ^ (g & (h ^ a))) + constants[index + 6] + words[index + 6])
    f = u32(f + t1)
    b = u32(t1 + rotr2_13_22(c) + ((c & d) | (e & (c | d))))

    t1 = u32(a + rotr6_11_25(f) + (h ^ (f & (g ^ h))) + constants[index + 7] + words[index + 7])
    e = u32(e + t1)
    a = u32(t1 + rotr2_13_22(b) + ((b & c) | (d & (b | c))))
  }
  return [a, b, c, d, e, f, g, h]
}

function expandSchedule(words: bigint[], firstWord: number): bigint[] {
  for (let index = firstWord; index < 64; index++) {
    const x = words[index - 2]
    const y = words[index - 15]
    const s1 = (x >> 17n) ^ (x << 15n) ^ (x >> 19n) ^ (x << 13n) ^ (x >> 10n)
    const s0 = (y >> 7n) ^ (y << 25n) ^ (y >> 18n) ^ (y << 14n) ^ (y >> 3n)
    words.push(u32(s1 + words[index - 7] + s0 + words[index - 16]))
  }
  return words
}

function sha256Init(data: Buffer): ShaState {
  const words: bigint[] = new Array(14).fill(0n)
  // int.from_bytes(data[:3], "big") - note the first word is 3 bytes wide.
  let w14 = 0n
  for (const byte of data.subarray(0, 3)) w14 = (w14 << 8n) | BigInt(byte)
  words.push(w14)
  words.push(BigInt(data.readUInt32BE(3)))

  for (let index = 16; index < 30; index++) {
    let value = SHA256_SCHEDULE[index - 16]
    if (index - 2 >= 14) {
      const s = sigma1(words[index - 2])
      value += index === 16 ? s ^ SHA256_SIGMA1_ADJUSTMENT : s
    }
    if (index - 7 >= 14) value += words[index - 7]
    if (index - 15 >= 14) {
      const s = sigma0(words[index - 15])
      value += index === 29 ? s ^ SHA256_SIGMA0_ADJUSTMENT : s
    }
    words.push(u32(value))
  }
  words.push(
    u32(
      sigma1(words[28]) +
        words[23] +
        sigma0(words[15]) +
        words[14] +
        SHA256_ADJUSTMENT -
        SHA256_ROUND_CONSTANTS[14],
    ),
  )
  expandSchedule(words, 31)

  const working = sha256Rounds(SHA256_STATE, words, 14)
  return working.map((v, i) => u32(SHA256_INITIAL[i] + v))
}

function sha256Digest(data: Buffer): Buffer {
  if (data.length < 7) {
    throw new Error(`token salt must contain at least seven bytes, got ${data.length}`)
  }
  const length = 57 + data.length
  const padded = Buffer.concat([
    data.subarray(7),
    Buffer.from([0x80]),
    Buffer.alloc(euclideanMod(55 - length, 64)),
  ])
  const lenBuf = Buffer.alloc(8)
  lenBuf.writeBigUInt64BE(BigInt(length * 8))
  const full = Buffer.concat([padded, lenBuf])

  let state = sha256Init(data.subarray(0, 7))
  for (let offset = 0; offset < full.length; offset += 64) {
    const words: bigint[] = []
    for (let i = 0; i < 16; i++) words.push(BigInt(full.readUInt32BE(offset + i * 4)))
    expandSchedule(words, 16)
    const working = sha256Rounds(state, words, 0)
    state = state.map((v, i) => u32(v + working[i]))
  }

  const out = Buffer.alloc(32)
  for (let i = 0; i < 8; i++) out.writeUInt32BE(Number(u32(state[i])), i * 4)
  return out
}

function cs64Digest(data: Buffer, digest: Buffer): [bigint, bigint] {
  let keyA = BigInt(digest.readUInt32LE(0))
  let keyB = BigInt(digest.readUInt32LE(4))
  keyA = u32(keyA | 1n)
  keyB = u32(keyB | 1n)
  const factors: bigint[] = [u32(keyA * CS64_FACTOR), u32(keyB * CS64_FACTOR)]

  const terms: bigint[] = []
  const offsets: bigint[] = []
  CS64_COEFFICIENTS.forEach(([coefficient, offset], index) => {
    terms.push(u32(coefficient * factors[index % 2]))
    offsets.push(offset)
  })

  const prefixed = Buffer.concat([Buffer.from([0x00]), data])
  wordsLE4Even(prefixed).forEach((word, index) => {
    let term = word * (index % 2 === 0 ? keyA : keyB)
    let offset = rol16(word) * (index % 2 === 0 ? 0xef0569fbn : 0xc31713dbn)
    if (index === 0) {
      term += CS64_ADJUSTMENT0 * factors[0]
      offset += CS64_ADJUSTMENT1
    }
    terms.push(u32(term))
    offsets.push(u32(offset))
  })

  let swapAcc = 0n
  let swapLast = 0n
  let state = 0n
  let acc = 0n
  for (let index = 0; index + 1 < terms.length; index += 2) {
    const x = terms[index]
    const y = terms[index + 1]

    let t = rol16(u32(offsets[index] + x))
    let q = u32(t * 0x76f50f34n)
    swapAcc = u32(swapAcc + (rol16(q) * 0xc3efea97n + q))
    t = rol16(u32(offsets[index + 1] + y))
    q = u32(t * 0x1881ce3cn)
    swapLast = u32(rol16(q) * 0x35bd1ec9n + q)
    swapAcc = u32(swapAcc + swapLast)

    state = u32(state * keyA + x)
    state = u32(rol16(state) * 0xcf98b111n)
    state = u32(rol16(state) * 0x87085b9fn)
    state = u32(rol16(state) * 0x12ceb96dn)
    state = u32(rol16(state) * 0x257e1d83n)
    acc = u32(acc + state)

    let t2 = u32(state * keyB + y)
    t2 = u32(rol16(t2) * 0xa27416f5n)
    t2 = u32(rol16(t2) * 0xd38396ffn)
    state = u32(rol16(u32(rol16(t2) * 0x7c932b89n)) * 0xbfa49f69n)
    acc = u32(acc + state)
  }
  return [u32(swapLast ^ state), u32(swapAcc ^ acc)]
}

/**
 * Build a Sec-MS-GEC v2 token for the supplied Unix time in seconds.
 */
export function generateSecMsGecV2(unixTime: number): string {
  let ticks = unixTime + WINDOWS_FILE_TIME_EPOCH
  ticks -= ticks % 300
  ticks *= 1e9 / 100

  const strToHash = `${Math.round(ticks)}${TRUSTED_CLIENT_TOKEN}`
  const data = Buffer.from(strToHash, 'ascii')

  const digest = sha256Digest(data)
  const [w1, w2] = cs64Digest(data, digest)
  const cs64 = Buffer.alloc(8)
  cs64.writeUInt32LE(Number(u32(w1)), 0)
  cs64.writeUInt32LE(Number(u32(w2)), 4)

  return `${cs64.toString('hex').toUpperCase()}-${digest.toString('hex').toUpperCase()}`
}

