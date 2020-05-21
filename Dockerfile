FROM gcr.io/distroless/static
ADD cluster-autoscaler-priority-helper /
ENTRYPOINT ["/cluster-autoscaler-priority-helper"]
