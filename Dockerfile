FROM gcr.io/distroless/static-debian12:nonroot

COPY kube-insight /usr/local/bin/kube-insight

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kube-insight"]
