package utils

import (
	"fmt"
	"strings"

	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	// clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func GetClientset(kubeconfig string, overrides *clientcmd.ConfigOverrides) (clientset.Interface, error) {
	var config *rest.Config
	var err error
	if kubeconfig == "" {
		config, err = rest.InClusterConfig()
		if err == rest.ErrNotInCluster {
			err = nil
			kubeconfig = clientcmd.RecommendedHomeFile
		}
	}

	if err == nil && config == nil {
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
			overrides,
		).ClientConfig()
	}

	if err != nil {
		return nil, err
	}
	// create the clientset
	clientset, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}

const (
	separator  string = "--"
	spotStr    string = "spot"
	nonSpotStr string = "ondemand"
)

type InstanceDetails struct {
	InstanceType     string
	AvailabilityZone string
	IsSpot           bool
}

func (i *InstanceDetails) FromString(s string) {
	parts := strings.SplitN(s, separator, 3)
	i.InstanceType = parts[0]
	i.AvailabilityZone = parts[1]
	i.IsSpot = (parts[2] == "spot")
	// fmt.Printf("InstanceDetails.FromString(%s) parts: %v\n", s, parts)
}

func (i InstanceDetails) String() string {
	s := fmt.Sprintf("%s%s%s", i.InstanceType, separator, i.AvailabilityZone)
	if i.IsSpot {
		return fmt.Sprintf("%s%s%s", s, separator, spotStr)
	} else {
		return fmt.Sprintf("%s%s%s", s, separator, nonSpotStr)
	}
}

func (i InstanceDetails) GetRegion() string {
	if len(i.AvailabilityZone) == 0 {
		return "Unknown"
	}
	return i.AvailabilityZone[0 : len(i.AvailabilityZone)-1]
}
