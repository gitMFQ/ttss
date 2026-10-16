# Go rewrite

This repository now includes a standalone Go implementation alongside the original Next.js source, so the existing behavior remains available for comparison while the Go app is evaluated.

## Run

```sh
cd go && go run ./cmd/server
```

The Go server listens on `:3000` by default; set `ADDR` to change it. For an optimized binary:

```sh
cd go
go build -o ms-read-aloud ./cmd/server
./ms-read-aloud
```

## Configuration

- `MS_RA_FORWARDER_TOKEN` (fallback: `TOKEN`): optional bearer token. If set, the browser UI requires login and TTS/Legado endpoints validate the token.
- `PUBLIC_BASE_URL`: optional canonical public URL (for example `https://tts.example.com`), useful behind a reverse proxy. Without it, Legado URLs are derived from the request.
- `ADDR`: listen address (default `:3000`).

History and generated audio remain browser-local in IndexedDB. The Go server does not store user history.

## Compatible endpoints

- `GET /api/voices?service=edge|ms`
- `GET /api/text-to-speech?text=...&voice=...&service=edge|ms` (legacy-compatible, Bearer auth when enabled)
- `POST /api/text-to-speech` (JSON body; used by the web UI to avoid putting text in request URLs)
- `GET /api/legado-import?...` (returns the Legado import JSON descriptor)
- `GET /api/config`, `POST /api/login`, `GET /api/qrcode?url=...`, `GET /healthz`

The Go UI supports both TTS channels, voice/locale selection, rate/pitch/volume, Microsoft safe mode/Omni/style/role/region/format/language skill, Legado links/QR, login, and local audio history. The old Next.js files are intentionally retained as reference during the rewrite.
