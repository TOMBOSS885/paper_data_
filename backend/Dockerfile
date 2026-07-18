FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/paper-kb ./cmd/server
RUN mkdir -p /tmp/uploads

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/paper-kb /app/paper-kb
COPY --from=build /src/internal/db/migrations /app/internal/db/migrations
COPY --from=build --chown=nonroot:nonroot /tmp/uploads /app/uploads
USER nonroot:nonroot
ENV SERVER_HOST=0.0.0.0 SERVER_PORT=8080 UPLOAD_DIR=/app/uploads
EXPOSE 8080
VOLUME ["/app/uploads"]
ENTRYPOINT ["/app/paper-kb"]
