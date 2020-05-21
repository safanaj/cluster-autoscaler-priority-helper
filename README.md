# cluster-autoscaler-priority-helper
Kubernernetes Cluster-Autoscaler Priority helper (for AWS only)

This project is designed to be deployed aside of cluster-autoscaler (CAS) that will manage AWS ASGs with the `priority` expander.
To have fully control on which instances will be created by CAS we have to keep ASGs with a single AZ.
In case we want to manage many instance types this can reach some AWS ASG limitation (in term of the number of ASG per region).
To laverage the issue with the number of ASG the setup using `mixed_instances_policy` options in the ASGs is supported, as implication we are losing some control over the instance type that will be choosen by CAS delegating the choice to AWS (that will do the prediction using the capacity-optimized strategy)

## The ClusterAutoscaler priorities ConfigMap (cluster-autoscaler-priority-expander)

The ClusterAutoscaler with the expander option set to `proirity` is looking into the `cluster-autoscaler-priority-expander` ConfigMap to look for what is the priority for every single ASG to decide which one prefer.

https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/expander/priority/readme.md

The idea is to keep this ConfigMap up-to-date using some criterias, like price, termination probability in case of spot instances, distribution across AZs and differentiate as much as possible the instance types.

## The Logic

Starting with a `base-priority` the helper is associating a value to every single ASG decreasing or increasing that `base-priority` value as a score, that at the end will represent the priority for a specific ASG.

There are flags to control coefficients/bonus and malus for several aspect in the score/priority calculation:

- --base-priority (1000): the starting value for the score calculation
- --malus-for-ondemand (500): on-demand are costly
- --bonus-for-spot (100): spot are cheaper than on-demand
- --malus-for-probability (150): AWS spot advisor data, it is used as coefficient for the termination probability index (from 0 to 5, means 5%, 5-10%, 10-15%, 15-20%, > 20%)
- --malus-for-nodes-distribution (10): instance type distribution across AZs
- --malus-for-nodes-distribution-az-only: (10): node distribution across AZs
- --malus-for-price (100): coefficient to evaluate the price, cheaper is better

## MixedInstancesPolicy and capacity-optimized strategy

TO DOCUMENT

IMPORTANT: doen't make any sense to use ASGs with MixedInstancesPolicy and strategy different by capacity-optimized.


## Build it

`make build` or `make static`

To build a docker image `make docker` , it will build a static binary and put into a gcr.io/distroless/static base image.

## Running it

It will need the Kubernetes permission for:
- to read nodes (get, list)
- read/write the output ConfigMap `cluster-autoscaler-priority-expander` (create,get,update)
- read/write the lease object, cluster-autoscaler-priority-helper-leader-lease, can be an endpoint, a configmap or a coordination/v1 lease (create,get,update)

From the AWS perspective the IAM role for the instance that is running it will require permission for:
- autoscaling.DescribeTagsPages
- autoscaling.DescribeAutoScalingGroupsPages
- autoscaling.DescribeLaunchConfigurations
- ec2.DescribeLaunchTemplateVersions
- ec2.DescribeSpotPriceHistoryPages

## Notes

This is initialized to work with kubernetes v1.14.8

```
go get k8s.io/api@kubernetes-1.14.8 k8s.io/apimachinery@kubernetes-1.14.8 k8s.io/client-go@kubernetes-1.14.8
go mod tidy -v && go mod vendor -v
```
