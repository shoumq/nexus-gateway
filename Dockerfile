FROM golang:1.25.7-alpine AS build

WORKDIR /app

RUN apk add --no-cache git

COPY nexus-gateway/go.mod nexus-gateway/go.sum ./nexus-gateway/
COPY nexus-ai/go.mod nexus-ai/go.sum ./nexus-ai/
COPY auth_service/go.mod auth_service/go.sum ./auth_service/

ENV GOPROXY=https://proxy.golang.org,direct
RUN cd /app/nexus-gateway && go mod download

COPY nexus-gateway ./nexus-gateway
COPY nexus-ai ./nexus-ai
COPY auth_service ./auth_service

RUN cd /app/nexus-gateway && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/bin/nexus-gateway .

FROM alpine:3.20

WORKDIR /app

COPY --from=build /app/bin/nexus-gateway /app/nexus-gateway

EXPOSE 8088

ENV PORT=8088
ENV NEXUS_AI_ADDR=nexus_ai:9091

ENTRYPOINT ["/app/nexus-gateway"]
