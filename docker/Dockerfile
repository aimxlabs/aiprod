FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /aiprod ./cmd/aiprod

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /aiprod /usr/local/bin/aiprod

VOLUME /data
EXPOSE 8600 2525 1993

ENTRYPOINT ["aiprod"]
CMD ["serve", "--"]
