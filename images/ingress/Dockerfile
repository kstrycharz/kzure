FROM golang:1.26-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /kzure ./cmd/kzure
FROM alpine:3.23@sha256:ff7f0c727dd796aa36787bfcd9a64bf35a6ce72a73451b3359fde849b6703d63
RUN adduser -D -u 10001 kzure && mkdir /data && chown kzure:kzure /data
COPY --from=build /kzure /usr/local/bin/kzure
USER kzure
ENTRYPOINT ["kzure","edge"]
