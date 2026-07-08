FROM golang:1.26.5-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o glint ./cmd/glint

FROM alpine:3.24.1
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/glint /usr/local/bin/glint
ENTRYPOINT ["glint"]
