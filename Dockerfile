ARG TARGETOS
ARG TARGETARCH
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/nowakeai/kube-insight"

ARG TARGETOS
ARG TARGETARCH
COPY ${TARGETOS}/${TARGETARCH}/kube-insight /usr/local/bin/kube-insight

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kube-insight"]
