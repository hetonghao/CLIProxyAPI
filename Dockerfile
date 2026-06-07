FROM golang:1.26-alpine AS builder

WORKDIR /app

ARG ALPINE_MAIN_REPO=https://mirrors.tuna.tsinghua.edu.cn/alpine/v3.23/main
ARG ALPINE_COMMUNITY_REPO=https://mirrors.tuna.tsinghua.edu.cn/alpine/v3.23/community
ARG GOPROXY=https://goproxy.cn,direct

RUN apk add --no-cache \
  --repository="${ALPINE_MAIN_REPO}" \
  --repository="${ALPINE_COMMUNITY_REPO}" \
  build-base

COPY go.mod go.sum ./

RUN GOPROXY="${GOPROXY}" go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" -o ./CLIProxyAPI ./cmd/server/

FROM alpine:3.23

ARG ALPINE_MAIN_REPO=https://mirrors.tuna.tsinghua.edu.cn/alpine/v3.23/main
ARG ALPINE_COMMUNITY_REPO=https://mirrors.tuna.tsinghua.edu.cn/alpine/v3.23/community

RUN apk add --no-cache \
  --repository="${ALPINE_MAIN_REPO}" \
  --repository="${ALPINE_COMMUNITY_REPO}" \
  tzdata

RUN mkdir /CLIProxyAPI

COPY --from=builder ./app/CLIProxyAPI /CLIProxyAPI/CLIProxyAPI

COPY config.example.yaml /CLIProxyAPI/config.example.yaml

WORKDIR /CLIProxyAPI

EXPOSE 8317

ENV TZ=Asia/Shanghai

RUN cp /usr/share/zoneinfo/${TZ} /etc/localtime && echo "${TZ}" > /etc/timezone

CMD ["./CLIProxyAPI"]
