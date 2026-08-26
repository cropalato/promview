FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web-build /src/web/dist ./web/dist
# Stamped so the running binary can say what it is. Without it promview_build_info
# reports "dev" everywhere and "did the rollout land" stays unanswerable from
# outside the cluster.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/promview ./cmd/promview

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 promview \
    && adduser -S -D -H -u 65532 -G promview promview
WORKDIR /app
COPY --from=go-build /out/promview /usr/local/bin/promview
COPY --from=web-build /src/web/dist /app/web
COPY migrations /app/migrations
USER 65532:65532
ENV PROMVIEW_LISTEN_ADDRESS=:8080
ENV PROMVIEW_WEB_DIRECTORY=/app/web
ENV PROMVIEW_MIGRATIONS_DIRECTORY=/app/migrations
EXPOSE 8080
ENTRYPOINT ["promview"]
