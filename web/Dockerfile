# web/Dockerfile —— 前端构建 + nginx 反代（构建上下文：./web）
FROM node:22-alpine AS build

WORKDIR /app

# 层缓存：先装依赖
COPY package.json package-lock.json* ./
RUN npm install

COPY . .
# 生产构建（输出到 dist/）
RUN npm run build

# ---- 运行态：nginx 提供静态资源并反代 /api ----
FROM nginx:1.27-alpine

# 默认 SPA 行为 + /api 反代到后端（见 nginx.conf）
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
