ARG NODE_IMAGE=node:22-alpine
ARG NGINX_IMAGE=nginx:1.27-alpine
FROM ${NODE_IMAGE} AS build
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /src
COPY frontend/package*.json ./
RUN npm config set registry ${NPM_REGISTRY} \
 && npm config set fetch-retries 5 \
 && npm config set fetch-retry-maxtimeout 120000 \
 && npm ci --no-audit --no-fund
COPY frontend .
RUN npm run build

FROM ${NGINX_IMAGE}
COPY --from=build /src/dist /usr/share/nginx/html
# 模板由镜像入口脚本用 NGINX_PORT/API_PORT 环境变量渲染成 conf.d/default.conf
COPY deploy/nginx.conf.template /etc/nginx/templates/default.conf.template
RUN rm -f /etc/nginx/conf.d/default.conf
HEALTHCHECK --interval=30s --timeout=3s CMD wget -q -O /dev/null "http://127.0.0.1:${NGINX_PORT:-80}/healthz" || exit 1
