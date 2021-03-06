package main

import (
	"fmt"
	"strings"
	"time"

	goflag "flag"
	flag "github.com/spf13/pflag"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/client/leaderelectionconfig"

	scorerconfig "github.com/safanaj/cluster-autoscaler-priority-helper/pkg/scorer/config"
)

type Flags struct {
	version bool

	kubeconfig             string
	autoDiscoverASGsByTags string
	overrides              *clientcmd.ConfigOverrides
	outConfigMapName       string

	leaderElection componentbaseconfig.LeaderElectionConfiguration

	scorerConfig                 scorerconfig.ScorerConfiguration
	spotAdvisorRefreshInterval   time.Duration
	asgDiscovererRefreshInterval time.Duration
	pricerRefreshInterval        time.Duration
	scorerRefreshInterval        time.Duration
}

func parseFlags() *Flags {
	flags := &Flags{}
	klog.InitFlags(nil)
	flags.overrides = &clientcmd.ConfigOverrides{}
	clientcmd.BindOverrideFlags(
		flags.overrides, flag.CommandLine,
		clientcmd.ConfigOverrideFlags{
			CurrentContext: clientcmd.FlagInfo{
				clientcmd.FlagContext, "", "", "The name of the kubeconfig context to use",
			},
		})

	flags.leaderElection = componentbaseconfig.LeaderElectionConfiguration{
		LeaderElect:   true,
		LeaseDuration: metav1.Duration{Duration: 60 * time.Second},
		RenewDeadline: metav1.Duration{Duration: 30 * time.Second},
		RetryPeriod:   metav1.Duration{Duration: 5 * time.Second},
		ResourceLock:  resourcelock.EndpointsResourceLock,
	}
	leaderelectionconfig.BindFlags(&flags.leaderElection, flag.CommandLine)

	flags.scorerConfig = scorerconfig.ScorerConfiguration{}
	scorerconfig.BindFlags(&flags.scorerConfig, flag.CommandLine)

	flag.BoolVar(&flags.version, "version", false, "Print version and exit")

	flag.StringVar(&flags.kubeconfig, clientcmd.RecommendedConfigPathFlag, "", "kubeconfig path")
	flag.StringVar(&flags.autoDiscoverASGsByTags, "auto-discover-asg-by-tags", "", "")
	flag.StringVar(&flags.outConfigMapName, "output-configmap", priorityConfigMapName, "")

	flag.DurationVar(&flags.spotAdvisorRefreshInterval, "spot-advisor-refresh-interval", 600*time.Second, "")
	flag.DurationVar(&flags.asgDiscovererRefreshInterval, "asg-discoverer-refresh-interval", 600*time.Second, "")
	flag.DurationVar(&flags.pricerRefreshInterval, "pricer-refresh-interval", 600*time.Second, "")
	flag.DurationVar(&flags.scorerRefreshInterval, "scorer-refresh-interval", 600*time.Second, "")

	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Parse()
	return flags
}

func parseAutoDiscoverASGsByTags(s string) map[string]string {
	parts := strings.Split(s, ":")
	if parts[0] != "asg" {
		panic(fmt.Errorf("auto-discover-asg-by-tags needs an `asg` prefix like --auto-discover-asg-by-tags=asg:tag1=val1,tag2,tag3=val3"))
	}
	res := make(map[string]string)
	for _, pair := range strings.Split(parts[1], ",") {
		tagParts := strings.Split(pair, "=")
		if len(tagParts) == 2 {
			res[tagParts[0]] = tagParts[1]
		} else {
			res[tagParts[0]] = ""
		}
	}
	return res
}
