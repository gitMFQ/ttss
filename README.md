# MS Read Aloud Forwarder (ms-text-to-speech)

一个文本转语音（TTS）服务，通过 Microsoft Edge“大声朗读”与微软翻译通道生成语音，并提供 API 供阅读 App 等第三方应用调用。仓库保留原有 Next.js 实现，并新增独立的 Go 全栈重写版（见 `go/`），可单独运行和部署。

## 功能特性

- ✅ 支持 Microsoft Edge 浏览器“大声朗读”TTS 接口
- ✅ 支持微软翻译免费通道（`service=ms`），无需 Azure 订阅密钥，且支持讲话风格（personality）
- ✅ 提供简洁的 Web 界面，支持文本输入、参数调节和历史记录
- ✅ 提供 RESTful API 接口，便于集成到第三方应用
- ✅ 支持 Token 认证（可选），防止滥用
- ✅ 多语言、多音色支持（基于 Edge TTS 服务）
- ✅ 可调节语速、音调、音量等参数

## 技术栈

- **框架**: Next.js 16.4.0（App Router）
- **语言**: TypeScript
- **UI**: React 19 + Radix UI + Tailwind CSS
- **状态管理**: React Hook Form + TanStack Query
- **验证**: Zod
- **其他**: WebSocket（实时通信）、Keyv（缓存）等
- **Go 重写版**: Go 1.23+、标准库 HTTP、Gorilla WebSocket；UI 使用嵌入式 HTML/CSS/JS

## Go 版本（全栈重写）

完整 Go 版本位于 [`go/`](go/)，包含嵌入式 Web UI、登录鉴权、Edge / 微软翻译 TTS、音色列表、Legado 导入与本地历史。原 Next.js 源码暂时保留，便于迁移期间对照。

```bash
cd go
go run ./cmd/server
```

默认监听 `:3000`。如原 Next.js 服务仍在该端口运行，可改用 `ADDR=:3100 go run ./cmd/server`。认证配置沿用 `MS_RA_FORWARDER_TOKEN`（优先）或 `TOKEN`；反向代理部署可设置 `PUBLIC_BASE_URL=https://你的域名`。更多说明见 [`go/README_GO.md`](go/README_GO.md)。

#### 直接下载预编译二进制

每次推送 `v*` 标签，GitHub Actions 会编译 Linux 的 `amd64` / `arm64` 二进制并发布到 Releases 页面：

```bash
# 版本号与文件名请到仓库 Releases 页面确认
curl -LO https://github.com/gitMFQ/ttss/releases/download/v1.0.0/ttss-linux-amd64
chmod +x ttss-linux-amd64
./ttss-linux-amd64
```

### Sec-MS-GEC v2（2026-10-19）

微软已废弃 Edge TTS 的 `Sec-MS-GEC` **v1** 令牌算法并切换到 v2。继续使用 v1 会在 WebSocket 握手阶段返回 **HTTP 403**。

v2 令牌格式为 `<16 位十六进制 CS64>-<64 位十六进制摘要>`（共 81 字符），由自研 SHA-256 与 CS64 双段摘要组成；`Sec-MS-GEC-Version` 也须由 `1-` 改为 `2-` 前缀。

> ⚠️ 该摘要**不是标准 SHA-256**（上游刻意扰动了轮常量与消息调度），无法用 `crypto/sha256`、`node:crypto` 等标准库实现，必须按上游算法移植。
>
> 💡 国内网络下遇到 403 通常**不是**被墙或地域限制：同域名同 IP 下 `voices/list` 接口仍返回 200，只有握手被拒。请优先升级到 v2，而非依赖代理绕过。

移植实现见 [`go/internal/tts/drm.go`](go/internal/tts/drm.go)（Go）与 [`service/edge-tts-service/drm.ts`](service/edge-tts-service/drm.ts)（TypeScript），二者均用上游 `edge-tts` 的参考向量做了逐位校验。

## 快速开始（原 Next.js 版本）

### 手动运行

确保已安装 Node.js（版本 18 或更高）和 Git。

```bash
# 克隆项目
git clone https://github.com/wxxxcxx/ms-ra-forwarder.git
cd ms-ra-forwarder

# 安装依赖（项目使用 yarn 4）
corepack enable
yarn install

# 构建
yarn build
```

启动生产服务：

```bash
# 推荐：直接调用 next，单进程，内存占用最低（实测约 90 MB）
node node_modules/next/dist/bin/next start
```

> ⚠️ **不建议用 `yarn start` 或 `npm run start` 启动生产服务。**
> 包管理器会额外常驻一个 Node 进程做转发，实测内存占用：`yarn start` 约 220 MB、
> `npm run start` 约 160 MB，而直接调用 `next` 仅约 90 MB。另外多出的中间进程
> 会干扰 `SIGTERM` 透传，停止服务时可能需要等超时强杀。
>
> `yarn start` / `npm run start` 仍可用于本地临时验证，长期运行请用上面的命令。

开发模式：

```bash
yarn dev
```

### 后台常驻运行（tmux）

用 tmux 让服务在后台常驻，退出 SSH 后也不会中断：

```bash
# 启动（会话名 ttss，直接调用 next，单进程）
tmux new-session -d -s ttss -c "$PWD" \
  "node node_modules/next/dist/bin/next start"
```

常用操作：

```bash
# 查看实时日志（按 Ctrl+B 再按 D 脱离，服务继续运行）
tmux attach -t ttss

# 只看日志，不进入会话
tmux capture-pane -pt ttss -S -50

# 停止服务
tmux kill-session -t ttss
```

> 💡 tmux 不是守护进程管理器，**服务器重启后服务不会自动拉起**。如需开机自启，请自行
> 配置 systemd 或 `cron @reboot`。
>
> 💡 日志默认只保留在 tmux 的滚动缓冲中，超出即丢失。需要留存日志可改为：
>
> ```bash
> tmux new-session -d -s ttss -c "$PWD" \
>   "node node_modules/next/dist/bin/next start 2>&1 | tee -a ttss.log"
> ```
>
> 服务默认不会把待合成的文本写入日志，长期留存无隐私顾虑（详见下文「日志」一节）。

## 使用说明

### Web 界面

访问 `http://your-server:3000`，你可以：

1. 选择“Edge 大声朗读”或“微软翻译”服务通道
2. 在文本框中输入要转换的文本
3. 选择语言、音色；微软翻译通道可配置区域、输出格式、稳定模式、风格/角色/强度、语言技能和 Omni
4. 可关闭稳定模式以启用手动风格、角色和输出格式；自动风格支持关键词规则及情绪桥接标签
5. 调节语速、音调、音量，点击“转换”试听语音
6. 生成历史记录会保存在侧边栏
7. 可直接点击“导入到阅读”将当前通道配置到阅读（legado）App

### API 调用

**接口地址**: `GET /api/text-to-speech`

**参数说明**:

| 参数 | 类型 | 必需 | 默认值 | 说明 |
|------|------|------|--------|------|
| text | string | 是 | - | 要转换的文本 |
| voice | string | 是 | - | 音色名称，如 `zh-CN-XiaoxiaoNeural` |
| volume | number | 否 | UI/MS：0；Edge 旧 API：100 | 音量，范围 -100 到 100；UI 的 0 表示默认音量 |
| rate | number | 否 | 0 | 语速，范围 -100 到 100 |
| pitch | number | 否 | 0 | 音调，范围 -100 到 100 |
| personality / style | string | 否 | - | 讲话风格；`style` 是参考插件兼容别名 |
| role | string | 否 | default | 角色（依音色 `RolePlayList`） |
| styleDegree | number | 否 | 1 | 风格强度，0–2 |
| region | string | 否 | auto | `auto`、`hd`、eastasia、southeastasia、northeurope、westeurope、westus、eastus |
| format | string | 否 | 24k/160k MP3 | 微软 TTS 输出格式；支持 MP3、Opus、AMR、PCM/WAV |
| langSkill | string | 否 | 音色默认语言 | 多语言/Omni 音色的语言技能，如 `en-US` |
| omni | boolean | 否 | false | 普通 Neural 音色转为 Dragon HD Omni（仅支持的音色生效） |
| safeMode / msSafeMode | boolean | 否 | true | 稳定模式：固定 24k/160k MP3、收敛 prosody、使用默认 style/role |
| autoStyleRules / as | string | 否 | - | 自动风格规则，每行 `style[关键词|关键词]` |
| service | string | 否 | edge | TTS 通道：`edge` 或 `ms`（微软翻译免费通道） |

**关于 `service=ms`（微软翻译免费通道）**：

- 通过微软翻译 App 接口换取短时 Azure TTS 令牌并自动缓存/刷新，**无需 Azure 订阅密钥**。
- UI 音色来自微软 voices/list 元数据，可按音色显示 `StyleList`、`RolePlayList`、`SecondaryLocaleList`；支持讲话风格、角色、风格强度、语言技能和 Omni。
- 稳定模式与桥接清理：清除 `[[emo:...]]`、`[[ms_style:...]]`、`[[mimo_ctx:...]]` 等内部标记，并可从情绪标签/行首提示选择该音色支持的风格；不会朗读控制标签。
- 支持 23 种微软输出格式。稳定模式固定 MP3；关闭稳定模式后才应用所选格式。Omni 会将易出问题的 Opus/AMR/48k 等格式回退至稳定 MP3。
- `region=auto` 使用令牌返回区域；`hd` 与 Omni 按参考插件规则映射。只允许官方区域枚举，不开放任意代理主机，以避免 SSRF。
- 该通道依赖微软翻译 App 接口，不保证长期可用；Edge 仍为默认通道。音色使用微软短名，如 `zh-CN-XiaoxiaoNeural`。

**示例请求**:

```bash
curl "http://localhost:3000/api/text-to-speech?voice=Microsoft+Server+Speech+Text+to+Speech+Voice+(zh-CN,+XiaoxiaoNeural)&volume=0&rate=0&pitch=0&text=你好"

# 微软翻译免费通道 + 讲话风格
curl "http://localhost:3000/api/text-to-speech?voice=zh-CN-XiaoxiaoNeural&service=ms&safeMode=false&style=cheerful&role=default&styleDegree=1.2&text=你好"

# 关闭稳定模式并使用音色情绪桥接标签（控制标签不会被朗读）
curl "http://localhost:3000/api/text-to-speech?voice=zh-CN-XiaoxiaoNeural&service=ms&safeMode=false&text=%5B%5Bemo%3Aangry%5D%5D%E5%88%AB%E8%BF%87%E6%9D%A5%EF%BC%81"
```

**成功响应**: 返回音频文件；`Content-Type` 随所选格式返回 `audio/mpeg`、`audio/ogg`、`audio/webm`、`audio/wav` 或 `audio/amr`。稳定模式下固定为 `audio/mpeg`。

**错误响应**:

- `400 Bad Request`: 缺少必需参数、数值越界或通道/区域/格式无效
- `401 Unauthorized`: Token 无效（如果启用了认证）
- `500 Internal Server Error`: 服务器内部错误

### 身份认证

本项目**没有内置的固定密码**。登录所需的「令牌」由你在启动服务时通过环境变量自行设定。

#### 令牌从哪来

启动服务时设置 `TOKEN`（或 `MS_RA_FORWARDER_TOKEN`）即可启用认证，值就是登录时该填的内容：

```bash
# 在 tmux 中启动，并设定令牌为 mySecret123（可换成任意字符串）
tmux new-session -d -s ttss -c "$PWD" \
  "TOKEN=mySecret123 node node_modules/next/dist/bin/next start"
```

之后：

- **Web 登录页**：填写 `mySecret123`
- **API 调用**：请求头携带 `Authorization: Bearer mySecret123`

#### 两种运行状态

| 环境变量 | 行为 |
|----------|------|
| **未设置**（默认） | 不启用认证。登录页填写任意非空内容即可进入，API 无需鉴权 |
| **已设置** | 登录页必须填写与环境变量**完全一致**的令牌；所有 API 请求必须携带正确的 `Authorization` 头 |

> 💡 `MS_RA_FORWARDER_TOKEN` 的优先级高于 `TOKEN`，两者同时设置时以 `MS_RA_FORWARDER_TOKEN`
> 为准。若修改 `TOKEN` 后发现未生效，请检查是否仍残留 `MS_RA_FORWARDER_TOKEN`。

#### API 鉴权

启用认证后，所有 API 请求必须在请求头中携带：

```http
Authorization: Bearer <TOKEN>
```

示例：

```bash
curl -H "Authorization: Bearer mySecret123" \
  "http://localhost:3000/api/text-to-speech?voice=zh-CN-XiaoxiaoNeural&text=你好"
```

> ⚠️ **修改令牌后必须重启服务**。环境变量在进程启动时读取，运行中无法更改：
>
> ```bash
> tmux kill-session -t ttss
> tmux new-session -d -s ttss -c "$PWD" \
>   "TOKEN=新令牌 node node_modules/next/dist/bin/next start"
> ```

#### 安全提示

- ⚠️ 认证强度有限：前端把令牌存入浏览器 `localStorage` 即视为已登录，实际拦截发生在
  服务端 API 路由。适用于自建小服务，**不建议作为强安全边界**。
- ⚠️ 用 `TOKEN=xxx node ...` 方式传参时，令牌会出现在进程命令行中，同机器的其他用户可通过
  `ps` 看到。如在意，可改用 `.env` 文件（Next.js 会自动加载）或 systemd 的
  `EnvironmentFile=`。
- ⚠️ 本服务基于 HTTP 明文传输，若暴露在公网，建议置于 HTTPS 反向代理之后，否则令牌可能被
  中间人截获。

## 部署到 Vercel

[![Deploy with Vercel](https://vercel.com/button)](https://vercel.com/new/clone?repository-url=https%3A%2F%2Fgithub.com%2Fwxxxcxx%2Fms-ra-forwarder&env=TOKEN&envDescription=%E8%AE%BF%E9%97%AE%E4%BB%A4%E7%89%8C&project-name=ms-ra-forwarder&repository-name=ms-ra-forwarder)

> 注意：Vercel 的 Serverless 函数可能因冷启动或超时导致 WebSocket 连接不稳定，不推荐作为生产环境首选。

## 环境变量

| 变量名 | 必需 | 说明 |
|--------|------|------|
| `TOKEN` | 否 | API 访问令牌，设置后启用认证 |
| `MS_RA_FORWARDER_TOKEN` | 否 | 同上，优先级更高 |
| `PORT` | 否 | 服务端口，默认 `3000`（注意：写在 `.env` 中无效，见下文） |
| `TTS_PROXY` | 否 | HTTP 代理，例如 `http://127.0.0.1:7890`，用于让 Edge TTS 的 WebSocket 通过 HTTP CONNECT 隧道出站。优先级最高；留空则直连 |
| `HTTPS_PROXY` | 否 | 同上，仅在未设置 `TTS_PROXY` 时生效 |
| `HTTP_PROXY` | 否 | 同上，仅在未设置 `TTS_PROXY` 和 `HTTPS_PROXY` 时生效 |
| `TTS_POOL_SIZE` | 否 | 维持多少个长连接，默认 `3`。并发请求量较大时可调到 5–10 |
| `TTS_IDLE_TIMEOUT_MIN` | 否 | 连接空闲多久后主动关闭（分钟），默认 `5` |
| `TTS_TURN_TIMEOUT_SEC` | 否 | 单个合成请求的总超时（秒，含排队时间），默认 `60` |
| `TTS_PING_INTERVAL_SEC` | 否 | WebSocket 心跳间隔（秒），默认 `30` |

> 💡 以上变量在 Go 版本里同样生效，变量名一致。

## Edge TTS 连接复用

WebSocket 握手是 Edge TTS 最大的延迟来源。为减少该开销，本服务内部维护一个长连接池：

- **连接复用**：每条 WebSocket 上串行处理多个 turn（服务端要求 FIFO），每个 turn 结束后立刻释放连接给下一个请求
- **健康检查**：默认每 30 秒发送一次 WebSocket ping；读超时为 70 秒，超时或出错即判定连接死亡并重建
- **空闲关闭**：连接空闲超过 `TTS_IDLE_TIMEOUT_MIN` 后主动断开
- **失败重试**：连接在请求被应答之前断开时，该请求会自动在新连接上重试一次
- **超时隔离**：单个 turn 超时或调用方取消时，该连接会被丢弃重建，避免残留帧污染后续请求

> 具体延迟取决于本机到微软服务器的网络质量，本仓库不再给出未经测量的固定数值；如需基准数据请自行压测。

国内网络下可能出现 WSS 握手数秒的尖峰（微软服务端排队）。若遭遇此问题，可设置 `TTS_PROXY=http://127.0.0.1:7890`（mihomo / Clash 默认端口）走 HTTP CONNECT 隧道出站；语音列表与合成会共用同一代理。

## 监听地址与端口

默认监听 **`0.0.0.0:3000`**，即所有网络接口的 3000 端口。可用启动参数或环境变量修改：

```bash
# 改端口（两种写法等价）
node node_modules/next/dist/bin/next start --port 8080
PORT=8080 node node_modules/next/dist/bin/next start

# 只监听本机（仅 127.0.0.1 可访问，外部无法连接）
node node_modules/next/dist/bin/next start --hostname 127.0.0.1

# 同时指定地址和端口
node node_modules/next/dist/bin/next start --hostname 127.0.0.1 --port 8080
```

| 设置项 | 参数 | 环境变量 | 默认值 |
|--------|------|----------|--------|
| 端口 | `-p, --port` | `PORT` | `3000` |
| 监听地址 | `-H, --hostname` | 无 | `0.0.0.0` |

> 💡 **`--hostname` 没有对应的环境变量**。设置 `HOSTNAME=127.0.0.1` 不会改变监听地址，
> 只能用 `--hostname` 参数。
>
> 💡 **参数优先于环境变量**。同时指定 `PORT=4400 ... --port 4500` 时，实际监听 `4500`。
>
> 💡 在 tmux 中启动时，把参数写在启动命令里即可：
>
> ```bash
> tmux new-session -d -s ttss -c "$PWD" \
>   "node node_modules/next/dist/bin/next start --hostname 127.0.0.1 --port 8080"
> ```

### 关于 `.env` 文件中的 `PORT`

⚠️ **`PORT` 写在 `.env` 文件里不生效**，必须用环境变量或命令行参数传入。

原因是 `next start` 在**解析命令行参数时**就从 `process.env` 读取 `PORT`（作为 `--port` 的
默认值），而 `.env` 文件是**在这之后**才被加载的，因此赶不上。

> 💡 对比：`TOKEN` 是在**请求处理时**才读取 `process.env`，所以写在 `.env` 里**是生效的**。

### 安全建议

- 若服务仅本机使用（如配合反向代理），建议绑定 `--hostname 127.0.0.1`，避免直接暴露到
  局域网或公网。
- 默认的 `0.0.0.0` 会监听所有网卡。若服务器有公网 IP 且未配置防火墙，服务将直接对外
  可访问；**未设置 `TOKEN` 时任何人都能调用**。

## 日志

### 日志级别无法用参数或环境变量控制

Node.js 的 `console.debug` 与 `console.log` 一样**直接写入 stdout**，不受任何「日志级别」
影响；`next start` 也没有 `--log-level` 之类的参数，Next.js 的 `logging` 配置项只在
`next dev` 下生效。因此运行时无法通过启动参数或环境变量调整日志级别。

要控制日志，只能在**构建期**剥离，见下节。

### 剥离调试日志（构建期）

服务在合成时会打印完整的 SSML 原文（包含待合成的文本内容），并逐条打印 WebSocket 收发
的二进制分片，输出量很大。如不需要，可在 `next.config.mjs` 中启用 `removeConsole`：

```js
/** @type {import('next').NextConfig} */
const nextConfig = {
    compiler: {
        removeConsole: {
            exclude: ['error'], // 保留 console.error，剥离其余 console.*
        },
    },
};

export default nextConfig;
```

> ⚠️ `removeConsole` 是**构建期**转换，修改后必须重新执行 `yarn build` 才会生效。
>
> ⚠️ 使用 `exclude: ['error']` 时，只有 `console.error` 会被保留。若项目里用
> `console.log` 打印错误（本项目早期代码如此），这些错误日志也会一并被删除。本项目已
> 将错误日志统一为 `console.error`，因此剥离后仍能正常看到合成失败的原因。
>
> 💡 若希望连错误日志一起剥离，可直接写 `removeConsole: true`。

## 重要更新历史

### 2026-10-19
- 🐛 **修复 Edge TTS 无法合成（WebSocket 握手 403）**：微软已废弃 `Sec-MS-GEC` v1 令牌，本项目 Go 与 TypeScript 两套实现均已移植 v2 算法（自研 SHA-256 + CS64 双段摘要），`Sec-MS-GEC-Version` 同步改为 `2-` 前缀。
- 💡 该问题与网络环境无关，无需代理即可直连使用（详见上文「Sec-MS-GEC v2」）。
- 🚀 新增 GitHub Actions 流水线：推送 `v*` 标签自动编译 Linux `amd64` / `arm64` 二进制并发布 Release。

### 2026-10-12
- ✨ 新增微软翻译免费通道（`service=ms`）：无需 Azure 订阅密钥，`personality` 讲话风格真正生效（映射为 `mstts:express-as`），令牌自动缓存与刷新。

### 2025-08-20
- ✅ 使用 **Next.js** 重构项目，修复了无法合成语音的问题。
- ⚠️ **部署时请删除原项目后重新部署**，旧版本不再兼容。

### 2023-04-19
- ⚠️ Azure 下线了演示页面的试用功能，Azure 版接口已失效，请迁移至 Edge 浏览器接口。

### 2022-11-18
- ✨ 添加词典文件支持（参考 [azure-tts-lexicon-cn](https://github.com/wxxxcxx/azure-tts-lexicon-cn/blob/main/lexicon.xml)）。

### 2022-07-17
- ✨ 添加 Azure TTS API 支持（需授权码，稳定性待验证）。

### 2022-07-02
- ⚠️ Edge 版 API 支持的音频格式调整为：
  - `webm-24khz-16bit-mono-opu`
  - `audio-24khz-48kbitrate-mono-mp3`
  - `audio-24khz-96kbitrate-mono-mp3`

### 2022-07-01
- ⚠️ 部署在中国大陆以外服务器时，仅支持 `webm-24khz-16bit-mono-opus` 格式。

### 2022-06-16
- ⚠️ Edge 浏览器接口不再支持设置讲话风格。

## 注意事项

- ⚠️ 本项目使用的是 Edge 浏览器的“大声朗读”接口与微软翻译免费通道，**不保证后续可用性和稳定性**，请勿用于关键业务。
- ⚠️ **本项目仅供学习和参考，请勿商用。**
- 💡 如果免费额度满足需求，建议支持 [Azure 官方 TTS 服务](https://azure.microsoft.com/products/ai-services/text-to-speech)。
- 💡 如果只需要为固定文本生成语音，可使用 [有声内容创作](https://speech.microsoft.com/audiocontentcreation)（功能更丰富）。

## 许可证

本项目采用 MIT 许可证，详情请见 [LICENSE](LICENSE) 文件。

## 致谢

感谢 JetBrains 提供的开源开发工具支持。

<img width="80" src="https://resources.jetbrains.com/storage/products/company/brand/logos/jb_beam.png" />
