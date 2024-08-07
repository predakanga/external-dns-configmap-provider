#syntax=docker/dockerfile:experimental

FROM gcr.io/distroless/static-debian10:nonroot
ENTRYPOINT ["/external-dns-configmap-provider"]
COPY external-dns/configmap-provider /
USER nonroot:nonroot
