# syntax=docker/dockerfile:1
# ─── Build stage ─────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS build

RUN apk add --no-cache nodejs npm git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build Tailwind CSS
RUN npm ci && npm run build:css

# Generate templ components
RUN go run github.com/a-h/templ/cmd/templ@latest generate -path ./internal/certdeck/views

# Compile binary (static, no CGO)
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w -X main.version=${VERSION:-dev}" \
      -o /unifi-cert-smash-deck \
      ./cmd/unificert

# ─── Runtime stage ────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /unifi-cert-smash-deck /unifi-cert-smash-deck
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# data/ is a volume — settings and state are written here
VOLUME ["/data"]

EXPOSE 8105

ENTRYPOINT ["/unifi-cert-smash-deck"]
