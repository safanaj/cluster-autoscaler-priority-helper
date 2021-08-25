ARG ARCH=amd64
FROM golang:1.17 as builder

ADD . /src

WORKDIR /src

RUN make install_deps deps static

FROM gcr.io/distroless/static:latest-${ARCH}
COPY --from=builder /src/cluster-autoscaler-priority-helper /
ENTRYPOINT ["/cluster-autoscaler-priority-helper"]
