# NGG — multi-stage build: Vue 3 UI + Go control plane in one small image.
# Build: docker build -t ngg:local .
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ internal/ pkg/ /src/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nggd ./cmd/nggd

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend /out/nggd /app/nggd
COPY --from=web /web/dist /app/web/dist
COPY configs/ngg.example.yaml /app/defaults/ngg.example.yaml
COPY configs/examples /app/defaults/examples
COPY scripts/docker-entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh && mkdir -p /app/configs /app/data

# 9090 control API, 18080 unified ingress, 18053 NGG DNS (host networking)
EXPOSE 9090 18080 18053
VOLUME ["/app/configs", "/app/data"]
ENTRYPOINT ["/app/entrypoint.sh"]
