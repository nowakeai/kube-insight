# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/kube-insight ./cmd/kube-insight

FROM gcr.io/google.com/cloudsdktool/google-cloud-cli:slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates google-cloud-cli-gke-gcloud-auth-plugin \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/kube-insight /usr/local/bin/kube-insight
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/kube-insight"]
