# ─── 基础镜像：安装依赖 ─────────────────────────────────────────
FROM node:trixie-slim AS base

# 设置工作目录
WORKDIR /app

# 启用 corepack 以使用 package.json 声明的 yarn 版本（yarn@4.18.1）
RUN corepack enable

# 仅拷贝依赖声明文件，利用 Docker 层缓存
COPY package.json yarn.lock .yarnrc.yml ./

# 使用 yarn 安装依赖（与项目 packageManager 保持一致）
# --immutable 保证 lockfile 与 package.json 严格一致
RUN yarn install --immutable

# ─── 构建阶段：Next.js 构建 ───────────────────────────────────────
FROM base AS builder

# 拷贝全部源码
COPY . .

RUN cat package.json

# 执行 Next.js 构建（standalone 输出，见 next.config.mjs）
RUN yarn build

# ─── 运行阶段：Standalone 输出 ────────────────────────────────────
FROM node:trixie-slim AS runner

WORKDIR /app

# 生产环境变量
ENV NODE_ENV=production
ENV PORT=3000

# 拷贝构建产物
# public 静态资源
COPY --from=builder /app/public ./public
# standalone 输出目录（包含 server.js 和 package.json）
COPY --from=builder /app/.next/standalone ./
# Next.js 静态文件
COPY --from=builder /app/.next/static ./.next/static

# 开放端口
EXPOSE 3000

# 启动（standalone 产物须用 node server.js，而非 next start）
CMD ["node", "server.js"]
