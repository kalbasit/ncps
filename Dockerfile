ARG GO_VERSION=1.23.3

## Build dbmate that will be used to create/migrate the database

FROM golang:$GO_VERSION AS dbmate_builder

ARG DBMATE_VERSION=2.22.0

RUN git clone -b v$DBMATE_VERSION https://github.com/amacneil/dbmate.git /app

WORKDIR /app

RUN go build -ldflags='-s -w' -trimpath -o /dist/bin/dbmate

RUN ldd /dist/bin/dbmate | tr -s [:blank:] '\n' | grep ^/ | xargs -I % install -D % /dist/%

## Build ncps

FROM golang:$GO_VERSION AS ncpc_builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go .
COPY cmd cmd
COPY pkg pkg

RUN go build -ldflags='-s -w' -trimpath -o /dist/bin/ncps

RUN ldd /dist/bin/ncps | tr -s [:blank:] '\n' | grep ^/ | xargs -I % install -D % /dist/%

## Finally, build the final image

FROM scratch

# Configure the final image
EXPOSE 8501
CMD ["/bin/ncps"]

# Configure and copy the migration files
ENV DBMATE_MIGRATIONS_DIR=/share/ncps/db/migrations
ENV DBMATE_NO_DUMP_SCHEMA=true
COPY ./db/migrations $DBMATE_MIGRATIONS_DIR

# Copy what we need from the dbmate builder.
COPY --from=dbmate_builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=dbmate_builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=dbmate_builder /dist /

# Copy what we need from the ncps builder.
COPY --from=ncpc_builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=ncpc_builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=ncpc_builder /dist /
