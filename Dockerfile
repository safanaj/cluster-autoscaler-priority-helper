FROM scratch
ADD cluster-autoscaler-priority-helper /
ENTRYPOINT ["/cluster-autoscaler-priority-helper"]
