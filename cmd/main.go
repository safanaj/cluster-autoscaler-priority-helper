package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/aws"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/nodes"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/scorer"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/spotadvisor"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/utils"
)

const priorityConfigMapName = "cluster-autoscaler-priority-expander"
const systemNamespace = "kube-system"

var version string

func main() {
	var err error
	flags := parseFlags()

	if flags.version {
		fmt.Printf("cluster-autoscaler-priority-helper version %s\n", version)
		os.Exit(0)
	}

	cs, err := utils.GetClientset(flags.kubeconfig, flags.overrides)
	if err != nil {
		panic(err.Error())
	}

	sad, err := spotadvisor.NewSpotAdvisor(flags.spotAdvisorRefreshInterval)
	if err != nil {
		panic(err.Error())
	}

	nd, err := nodes.NewNodesDistribution(cs)
	if err != nil {
		panic(err.Error())
	}

	asgD, err := aws.NewASGDiscoverer(flags.asgDiscovererRefreshInterval,
		parseAutoDiscoverASGsByTags(flags.autoDiscoverASGsByTags))
	if err != nil {
		panic(err.Error())
	}

	pricer, err := aws.NewPricer(flags.pricerRefreshInterval)
	if err != nil {
		panic(err.Error())
	}

	scorer := scorer.NewScorer(
		context.Background(), flags.leaderElection,
		cs, flags.outConfigMapName, systemNamespace, flags.scorerRefreshInterval,
		sad, asgD, nd, pricer,
		flags.scorerConfig)

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		scorer.Exit()
	}()

	scorer.Run()
}
