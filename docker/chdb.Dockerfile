ARG TARGETOS
ARG TARGETARCH
FROM gcr.io/distroless/base-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/nowakeai/kube-insight"

ARG TARGETOS
ARG TARGETARCH
COPY ${TARGETOS}/${TARGETARCH}/kube-insight /usr/local/bin/kube-insight
COPY build/chdb-runtime/libchdb-linux-${TARGETARCH}.so /usr/local/lib/libchdb.so

ENV LD_LIBRARY_PATH=/usr/local/lib
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kube-insight"]
