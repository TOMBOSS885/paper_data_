ARG GO_IMAGE=golang:1.22-alpine
FROM ${GO_IMAGE} AS build
ARG GOPROXY_URL=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY_URL}
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/paper-kb ./cmd/server
RUN mkdir -p /tmp/uploads

FROM scratch
WORKDIR /app
COPY --from=build /out/paper-kb /app/paper-kb
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /tmp/uploads /app/uploads
USER 65532:65532
ENV SERVER_HOST=0.0.0.0 SERVER_PORT=8080 UPLOAD_DIR=/app/uploads
EXPOSE 8080
VOLUME ["/app/uploads"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app/paper-kb", "healthcheck"]
ENTRYPOINT ["/app/paper-kb"]
