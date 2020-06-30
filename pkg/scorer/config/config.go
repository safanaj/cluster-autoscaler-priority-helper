package config

import (
	"github.com/spf13/pflag"
)

const (
	basePriority                   = 1000
	malusForOnDemand               = 500
	bonusForSpot                   = 100
	malusForProbability            = 150
	malusForNodeDistribution       = 10
	malusForNodeDistributionAZOnly = 10
	malusForPrice                  = 100

	hintsConfigMapName = "cluster-autoscaler-priority-hints"
)

type ScorerConfiguration struct {
	BasePriority                   int
	MalusForOnDemand               int
	BonusForSpot                   int
	MalusForProbability            int
	MalusForNodeDistribution       int
	MalusForNodeDistributionAZOnly int
	MalusForPrice                  int

	IgnoreAZs          bool
	HintsConfigMapName string
}

func BindFlags(sc *ScorerConfiguration, fs *pflag.FlagSet) {
	fs.IntVar(&sc.BasePriority, "base-priority", basePriority, "")
	fs.IntVar(&sc.MalusForOnDemand, "malus-for-ondemand", malusForOnDemand, "")
	fs.IntVar(&sc.BonusForSpot, "bonus-for-spot", bonusForSpot, "")
	fs.IntVar(&sc.MalusForProbability, "malus-for-probability", malusForProbability, "")
	fs.IntVar(&sc.MalusForNodeDistribution, "malus-for-nodes-distribution", malusForNodeDistribution, "")
	fs.IntVar(&sc.MalusForNodeDistributionAZOnly, "malus-for-nodes-distribution-az-only", malusForNodeDistributionAZOnly, "")
	fs.IntVar(&sc.MalusForPrice, "malus-for-price", malusForPrice, "")
	fs.BoolVar(&sc.IgnoreAZs, "ignore-availability-zones", false, "")
	fs.StringVar(&sc.HintsConfigMapName, "hints-configmap", hintsConfigMapName, "")
}
