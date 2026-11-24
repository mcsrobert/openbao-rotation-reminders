FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.4-trixie@sha256:c393c00864b56f4404b3fe4d029e47742fbb53ee11506521d2f3377009ad7a3a AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/openbao-rotation-reminders ./cmd/openbao-rotation-reminders

FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
LABEL org.opencontainers.image.source="https://github.com/mcsrobert/openbao-rotation-reminders"
LABEL org.opencontainers.image.description="Daily ntfy digest of OpenBao KV v2 secrets due for rotation"
LABEL org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/openbao-rotation-reminders /usr/local/bin/openbao-rotation-reminders
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/openbao-rotation-reminders"]
