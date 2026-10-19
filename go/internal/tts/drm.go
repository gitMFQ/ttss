package tts

// Sec-MS-GEC v2 token generation.
//
// Ported from edge-tts (rany2/edge-tts) src/edge_tts/drm.py.
// Microsoft replaced the v1 plain SHA-256 token with a v2 token that combines a
// modified SHA-256 digest with a custom CS64 digest, formatted as
// "<16 hex>-<64 hex>". Servers reject v1 tokens with HTTP 403 on handshake.
//
// Note: the digest here is NOT standard SHA-256. Upstream deliberately perturbs
// the schedule and round constants, so crypto/sha256 cannot be used.

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// Code generated from edge-tts drm.py constants. DO NOT EDIT.

var sha256RoundConstants = [64]uint32{
	0x428A2F98, 0x71374491, 0xB5C0FBCF, 0xE9B5DBA5,
	0x3956C25B, 0x59F111F1, 0x923F82A4, 0xAB1C5ED5,
	0xD807AA98, 0x12835B01, 0x243185BE, 0x550C7DC3,
	0x72BE5D74, 0x80DEB1FE, 0x9BDC06A7, 0xC19BF174,
	0xE49B69C1, 0xEFBE4786, 0x0FC19DC6, 0x240CA1CC,
	0x2DE92C6F, 0x4A7484AA, 0x5CB0A9DC, 0x76F988DA,
	0x983E5152, 0xA831C66D, 0xB00327C8, 0xBF597FC7,
	0xC6E00BF3, 0xD5A79147, 0x06CA6351, 0x14292967,
	0x27B70A85, 0x2E1B2138, 0x4D2C6DFC, 0x53380D13,
	0x650A7354, 0x766A0ABB, 0x81C2C92E, 0x92722C85,
	0xA2BFE8A1, 0xA81A664B, 0xC24B8B70, 0xC76C51A3,
	0xD192E819, 0xD6990624, 0xF40E3585, 0x106AA070,
	0x19A4C116, 0x1E376C08, 0x2748774C, 0x34B0BCB5,
	0x391C0CB3, 0x4ED8AA4A, 0x5B9CCA4F, 0x682E6FF3,
	0x748F82EE, 0x78A5636F, 0x84C87814, 0x8CC70208,
	0x90BEFFFA, 0xA4506CEB, 0xBEF9A3F7, 0xC67178F2,
}

var sha256Schedule = [14]uint32{
	0xEB24BF9E, 0x1558691F, 0xA0C09EE2, 0x0CFA32BA,
	0xA622EE8C, 0xD1DD9491, 0xC0DD25A0, 0xF9E70AB6,
	0xF0C31573, 0x7058CE4E, 0x7188E606, 0x00E5AB33,
	0xD258B060, 0x72766564,
}

var cs64Coefficients = [14][2]uint32{
	{0xE82E91B1, 0xB32700D0},
	{0x44DE24C6, 0x88DFF51D},
	{0x3BEB65BC, 0x29514038},
	{0xC89C5933, 0xB328CBDF},
	{0x8D1EC211, 0xD405B4D5},
	{0x314EF5D5, 0x26E55F3C},
	{0xE92491B1, 0x607801C6},
	{0xF0A5B9D5, 0x82B2C1FB},
	{0x06064CF3, 0xFA51A3DA},
	{0xDA79FA60, 0x207CC464},
	{0xBED20560, 0x3C3C74F3},
	{0x89F9C9F8, 0xD2D14B61},
	{0x35FA38C6, 0xBB04A2C1},
	{0xCB49E3C6, 0xC5626167},
}

const (
	sha256Adjustment   = 0xC9DC06A7
	sha256Sigma0Adjust = 0x059C0B80
	sha256Sigma1Adjust = 0x000B92C0
	cs64Factor         = 0x82893933
	cs64Adjustment0    = 0xF2F90B1A
	cs64Adjustment1    = 0x0B1A0000
)

var sha256InitialSeed = [8]uint32{0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A, 0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19}
var sha256StateSeed = [8]uint32{0xFD9FBEA2, 0x91243CE7, 0xBE499094, 0xCD2E1E0A, 0x6C006F49, 0x04BD35AB, 0xFEB0A7E4, 0x03EFFBD2}

const u32Mask = 0xFFFFFFFF

// windowsFileTimeEpoch is the seconds offset between the Unix and Windows epochs.
const windowsFileTimeEpoch = 11644473600

// euclideanMod returns a non-negative modulus, matching Python's % operator.
// Go's % keeps the sign of the dividend, which breaks the padding maths below.
func euclideanMod(a, b int) int {
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}

func rol16(value uint32) uint32 {
	return ((value << 16) | (value >> 16)) & u32Mask
}

// wordsLE4Even splits data into little-endian 32-bit words, zero-padded to an even count.
func wordsLE4Even(data []byte) []uint32 {
	padded := make([]byte, len(data))
	copy(padded, data)
	if r := len(padded) % 4; r != 0 {
		padded = append(padded, make([]byte, 4-r)...)
	}
	if len(padded) == 0 {
		return nil
	}
	words := make([]uint32, 0, len(padded)/4+1)
	for i := 0; i < len(padded); i += 4 {
		words = append(words, binary.LittleEndian.Uint32(padded[i:i+4]))
	}
	if len(words)%2 != 0 {
		words = append(words, 0)
	}
	return words
}

func sigma0(value uint32) uint32 {
	// Combined rotations, truncated once (matches Python semantics).
	return uint32(((uint64(value) >> 7) ^ (uint64(value) << 25) ^ (uint64(value) >> 18) ^ (uint64(value) << 14) ^ (uint64(value) >> 3)) & u32Mask)
}

func sigma1(value uint32) uint32 {
	return uint32(((uint64(value) >> 17) ^ (uint64(value) << 15) ^ (uint64(value) >> 19) ^ (uint64(value) << 13) ^ (uint64(value) >> 10)) & u32Mask)
}

// rotrE computes the SHA-256 big-sigma-one rotations using 64-bit arithmetic.
// Multiplying by 2**32+1 duplicates the word so a right shift yields ROTR's low bits.
func rotr6_11_25(e uint32) uint64 {
	v := uint64(e) * 0x100000001
	return (v >> 6) ^ (v >> 11) ^ (v >> 25)
}

func rotr2_13_22(a uint32) uint64 {
	v := uint64(a) * 0x100000001
	return (v >> 2) ^ (v >> 13) ^ (v >> 22)
}

type shaState [8]uint32

func sha256Rounds(state shaState, words []uint32, firstRound int) shaState {
	a, b, c, d, e, f, g, h := state[0], state[1], state[2], state[3], state[4], state[5], state[6], state[7]
	constants := sha256RoundConstants[:]
	if firstRound == 14 {
		constants = sha256BlockConstants()
	}
	mask := uint64(u32Mask)

	aligned := 64
	if v := ((firstRound + 7) / 8) * 8; v < aligned {
		aligned = v
	}
	for index := firstRound; index < aligned; index++ {
		t1 := (uint64(h) + rotr6_11_25(e) + uint64(g^(e&(f^g))) + uint64(constants[index]) + uint64(words[index])) & mask
		t2 := (rotr2_13_22(a) + uint64((a&b)|(c&(a|b)))) & mask
		// Python assigns the whole tuple at once, so e_new uses the OLD d.
		// Doing this as two Go statements would read the already-updated d.
		a, b, c, d, e, f, g, h = uint32((t1+t2)&mask), a, b, c, uint32((uint64(d)+t1)&mask), e, f, g
	}
	for index := aligned; index < 64; index += 8 {
		t1 := (uint64(h) + rotr6_11_25(e) + uint64(g^(e&(f^g))) + uint64(constants[index]) + uint64(words[index])) & mask
		d = uint32((uint64(d) + t1) & mask)
		h = uint32((t1 + rotr2_13_22(a) + uint64((a&b)|(c&(a|b)))) & mask)

		t1 = (uint64(g) + rotr6_11_25(d) + uint64(f^(d&(e^f))) + uint64(constants[index+1]) + uint64(words[index+1])) & mask
		c = uint32((uint64(c) + t1) & mask)
		g = uint32((t1 + rotr2_13_22(h) + uint64((h&a)|(b&(h|a)))) & mask)

		t1 = (uint64(f) + rotr6_11_25(c) + uint64(e^(c&(d^e))) + uint64(constants[index+2]) + uint64(words[index+2])) & mask
		b = uint32((uint64(b) + t1) & mask)
		f = uint32((t1 + rotr2_13_22(g) + uint64((g&h)|(a&(g|h)))) & mask)

		t1 = (uint64(e) + rotr6_11_25(b) + uint64(d^(b&(c^d))) + uint64(constants[index+3]) + uint64(words[index+3])) & mask
		a = uint32((uint64(a) + t1) & mask)
		e = uint32((t1 + rotr2_13_22(f) + uint64((f&g)|(h&(f|g)))) & mask)

		t1 = (uint64(d) + rotr6_11_25(a) + uint64(c^(a&(b^c))) + uint64(constants[index+4]) + uint64(words[index+4])) & mask
		h = uint32((uint64(h) + t1) & mask)
		d = uint32((t1 + rotr2_13_22(e) + uint64((e&f)|(g&(e|f)))) & mask)

		t1 = (uint64(c) + rotr6_11_25(h) + uint64(b^(h&(a^b))) + uint64(constants[index+5]) + uint64(words[index+5])) & mask
		g = uint32((uint64(g) + t1) & mask)
		c = uint32((t1 + rotr2_13_22(d) + uint64((d&e)|(f&(d|e)))) & mask)

		t1 = (uint64(b) + rotr6_11_25(g) + uint64(a^(g&(h^a))) + uint64(constants[index+6]) + uint64(words[index+6])) & mask
		f = uint32((uint64(f) + t1) & mask)
		b = uint32((t1 + rotr2_13_22(c) + uint64((c&d)|(e&(c|d)))) & mask)

		t1 = (uint64(a) + rotr6_11_25(f) + uint64(h^(f&(g^h))) + uint64(constants[index+7]) + uint64(words[index+7])) & mask
		e = uint32((uint64(e) + t1) & mask)
		a = uint32((t1 + rotr2_13_22(b) + uint64((b&c)|(d&(b|c)))) & mask)
	}
	return shaState{a, b, c, d, e, f, g, h}
}

func sha256BlockConstants() []uint32 {
	out := make([]uint32, len(sha256RoundConstants))
	copy(out, sha256RoundConstants[:])
	out[14] = sha256Adjustment
	return out
}

func expandSha256Schedule(words []uint32, firstWord int) []uint32 {
	mask := uint64(u32Mask)
	for index := firstWord; index < 64; index++ {
		x, y := words[index-2], words[index-15]
		s1 := (uint64(x) >> 17) ^ (uint64(x) << 15) ^ (uint64(x) >> 19) ^ (uint64(x) << 13) ^ (uint64(x) >> 10)
		s0 := (uint64(y) >> 7) ^ (uint64(y) << 25) ^ (uint64(y) >> 18) ^ (uint64(y) << 14) ^ (uint64(y) >> 3)
		words = append(words, uint32((s1+uint64(words[index-7])+s0+uint64(words[index-16]))&mask))
	}
	return words
}

func sha256Init(data []byte) shaState {
	mask := uint64(u32Mask)
	words := make([]uint32, 0, 64)
	words = append(words, make([]uint32, 14)...)
	words = append(words, beUint32(data[:3]))
	words = append(words, binary.BigEndian.Uint32(data[3:7]))

	for index := 16; index < 30; index++ {
		value := uint64(sha256Schedule[index-16])
		if index-2 >= 14 {
			if index == 16 {
				value += uint64(sigma1(words[index-2]) ^ sha256Sigma1Adjust)
			} else {
				value += uint64(sigma1(words[index-2]))
			}
		}
		if index-7 >= 14 {
			value += uint64(words[index-7])
		}
		if index-15 >= 14 {
			if index == 29 {
				value += uint64(sigma0(words[index-15]) ^ sha256Sigma0Adjust)
			} else {
				value += uint64(sigma0(words[index-15]))
			}
		}
		words = append(words, uint32(value&mask))
	}
	words = append(words, uint32((uint64(sigma1(words[28]))+uint64(words[23])+uint64(sigma0(words[15]))+
		uint64(words[14])+sha256Adjustment-uint64(sha256RoundConstants[14]))&mask))
	words = expandSha256Schedule(words, 31)

	working := sha256Rounds(shaState(sha256StateSeed), words, 14)
	var out shaState
	for i := 0; i < 8; i++ {
		out[i] = uint32((uint64(sha256InitialSeed[i]) + uint64(working[i])) & mask)
	}
	return out
}

// beUint32 reads up to 4 bytes big-endian, right-aligned (matches int.from_bytes semantics).
func beUint32(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = (v << 8) | uint32(c)
	}
	return v
}

func sha256Digest(data []byte) ([]byte, error) {
	if len(data) < 7 {
		return nil, fmt.Errorf("token salt must contain at least seven bytes, got %d", len(data))
	}
	mask := uint64(u32Mask)
	length := 57 + len(data)
	padded := make([]byte, 0, 128)
	padded = append(padded, data[7:]...)
	padded = append(padded, 0x80)
	// Python's % yields a non-negative result; Go's does not, so normalise first.
	padded = append(padded, make([]byte, euclideanMod(55-length, 64))...)
	var lb [8]byte
	binary.BigEndian.PutUint64(lb[:], uint64(length*8))
	padded = append(padded, lb[:]...)

	state := sha256Init(data[:7])
	for offset := 0; offset < len(padded); offset += 64 {
		words := make([]uint32, 0, 64)
		for i := 0; i < 16; i++ {
			words = append(words, binary.BigEndian.Uint32(padded[offset+i*4:offset+i*4+4]))
		}
		words = expandSha256Schedule(words, 16)
		working := sha256Rounds(state, words, 0)
		for i := 0; i < 8; i++ {
			state[i] = uint32((uint64(state[i]) + uint64(working[i])) & mask)
		}
	}
	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint32(out[i*4:i*4+4], state[i])
	}
	return out, nil
}

func cs64Digest(data []byte, digest []byte) (uint32, uint32) {
	mask := uint64(u32Mask)
	keyA := binary.LittleEndian.Uint32(digest[0:4])
	keyB := binary.LittleEndian.Uint32(digest[4:8])
	keyA |= 1
	keyB |= 1
	factors := [2]uint32{(keyA * cs64Factor) & u32Mask, (keyB * cs64Factor) & u32Mask}

	terms := make([]uint32, 0, len(cs64Coefficients)+8)
	offsets := make([]uint32, 0, len(cs64Coefficients)+8)
	for index, c := range cs64Coefficients {
		terms = append(terms, (c[0]*factors[index%2])&u32Mask)
		offsets = append(offsets, c[1])
	}

	prefixed := make([]byte, 0, len(data)+1)
	prefixed = append(prefixed, 0x00)
	prefixed = append(prefixed, data...)
	for index, word := range wordsLE4Even(prefixed) {
		term := uint64(word) * uint64(keyA)
		offset := uint64(rol16(word)) * 0xEF0569FB
		if index%2 != 0 {
			term = uint64(word) * uint64(keyB)
			offset = uint64(rol16(word)) * 0xC31713DB
		}
		if index == 0 {
			term += uint64(cs64Adjustment0) * uint64(factors[0])
			offset += cs64Adjustment1
		}
		terms = append(terms, uint32(term&mask))
		offsets = append(offsets, uint32(offset&mask))
	}

	var swapAcc, swapLast, state, acc uint32
	for index := 0; index+1 < len(terms); index += 2 {
		x, y := uint64(terms[index]), uint64(terms[index+1])

		t := rol16(uint32((uint64(offsets[index]) + x) & mask))
		q := (uint64(t) * 0x76F50F34) & mask
		swapAcc = uint32((uint64(swapAcc) + (uint64(rol16(uint32(q)))*0xC3EFEA97 + q)) & mask)
		t = rol16(uint32((uint64(offsets[index+1]) + y) & mask))
		q = (uint64(t) * 0x1881CE3C) & mask
		swapLast = uint32((uint64(rol16(uint32(q)))*0x35BD1EC9 + q) & mask)
		swapAcc = uint32((uint64(swapAcc) + uint64(swapLast)) & mask)

		state = uint32((uint64(state)*uint64(keyA) + x) & mask)
		state = uint32((uint64(rol16(state)) * 0xCF98B111) & mask)
		state = uint32((uint64(rol16(state)) * 0x87085B9F) & mask)
		state = uint32((uint64(rol16(state)) * 0x12CEB96D) & mask)
		state = uint32((uint64(rol16(state)) * 0x257E1D83) & mask)
		acc = uint32((uint64(acc) + uint64(state)) & mask)

		t2 := uint32((uint64(state)*uint64(keyB) + y) & mask)
		t2 = uint32((uint64(rol16(t2)) * 0xA27416F5) & mask)
		t2 = uint32((uint64(rol16(t2)) * 0xD38396FF) & mask)
		state = uint32((uint64(rol16(uint32((uint64(rol16(t2))*0x7C932B89)&mask))) * 0xBFA49F69) & mask)
		acc = uint32((uint64(acc) + uint64(state)) & mask)
	}
	return swapLast ^ state, swapAcc ^ acc
}

// generateSecMSGECv2 builds a Sec-MS-GEC v2 token for the supplied Unix time.
func generateSecMSGECv2(unixTime float64) (string, error) {
	ticks := unixTime + windowsFileTimeEpoch
	ticks -= math.Mod(ticks, 300)
	ticks *= 1e9 / 100
	if math.IsNaN(ticks) || math.IsInf(ticks, 0) {
		return "", fmt.Errorf("Sec-MS-GEC: non-finite tick value from unix time %v", unixTime)
	}

	strToHash := fmt.Sprintf("%.0f%s", ticks, trustedClientToken)
	data := []byte(strToHash)

	digest, err := sha256Digest(data)
	if err != nil {
		return "", err
	}
	w1, w2 := cs64Digest(data, digest)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[0:4], w1)
	binary.LittleEndian.PutUint32(buf[4:8], w2)
	cs64 := strings.ToUpper(hex.EncodeToString(buf))
	return cs64 + "-" + strings.ToUpper(hex.EncodeToString(digest)), nil
}
