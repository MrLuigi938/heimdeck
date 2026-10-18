# ---- Frontend ----
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Backend (one static binary with the frontend embedded) ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/heimdeck . \
 && mkdir -p /out/data

# ---- Runtime ----
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/heimdeck /heimdeck
COPY --from=build /out/data /data
ENV HEIMDECK_DATA=/data HEIMDECK_ADDR=:3000
EXPOSE 3000
VOLUME /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/heimdeck", "healthcheck"]
ENTRYPOINT ["/heimdeck"]
