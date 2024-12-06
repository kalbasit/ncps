ARG GO_VERSION=1.23.3
FROM golang:$GO_VERSION AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go .
COPY cmd cmd
COPY pkg pkg

RUN go build -ldflags='-s -w' -trimpath -o /dist/app/ncps

RUN ldd /dist/app/ncps | tr -s [:blank:] '\n' | grep ^/ | xargs -I % install -D % /dist/%

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=builder /dist /

WORKDIR /app

EXPOSE 8501

CMD ["/app/ncps"]
