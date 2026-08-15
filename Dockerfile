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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/promview ./cmd/promview

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S promview && adduser -S -G promview promview
WORKDIR /app
COPY --from=go-build /out/promview /usr/local/bin/promview
COPY --from=web-build /src/web/dist /app/web
COPY migrations /app/migrations
USER promview
ENV PROMVIEW_LISTEN_ADDRESS=:8080
ENV PROMVIEW_WEB_DIRECTORY=/app/web
ENV PROMVIEW_MIGRATIONS_DIRECTORY=/app/migrations
EXPOSE 8080
ENTRYPOINT ["promview"]
