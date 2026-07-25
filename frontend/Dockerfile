ARG NODE_IMAGE=node:22-alpine
ARG NGINX_IMAGE=nginx:1.27-alpine
FROM ${NODE_IMAGE} AS build
WORKDIR /src
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund
COPY frontend .
RUN npm run build

FROM ${NGINX_IMAGE}
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /src/dist /usr/share/nginx/html
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
RUN chown -R app:app /usr/share/nginx/html /var/cache/nginx /var/run /etc/nginx/conf.d
USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
