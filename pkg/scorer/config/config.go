package config

import (
	"github.com/spf13/pflag"
)

const (
	malusForOnDemand               = 500
	bonusForSpot                   = 100
	malusForProbability            = 150
	malusForNodeDistribution       = 10
	malusForNodeDistributionAZOnly = 10
	malusForPrice                  = 100
)

type ScorerConfiguration struct {
	MalusForOnDemand               int
	BonusForSpot                   int
	MalusForProbability            int
	MalusForNodeDistribution       int
	MalusForNodeDistributionAZOnly int
	MalusForPrice                  int
}

func BindFlags(sc *ScorerConfiguration, fs *pflag.FlagSet) {
	fs.IntVar(&sc.MalusForOnDemand, "malus-for-ondemand", malusForOnDemand, "")
	fs.IntVar(&sc.BonusForSpot, "bonus-for-spot", bonusForSpot, "")
	fs.IntVar(&sc.MalusForProbability, "malus-for-probability", malusForProbability, "")
	fs.IntVar(&sc.MalusForNodeDistribution, "malus-for-nodes-distribution", malusForNodeDistribution, "")
	fs.IntVar(&sc.MalusForNodeDistributionAZOnly, "malus-for-nodes-distribution-az-only", malusForNodeDistributionAZOnly, "")
	fs.IntVar(&sc.MalusForPrice, "malus-for-price", malusForPrice, "")
}
