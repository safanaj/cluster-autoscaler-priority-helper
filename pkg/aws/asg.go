package aws

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"k8s.io/klog"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/fetcher"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/utils"
)

// this is global and shared with the spotPricer
var (
	knownInstanceTypesListMu sync.RWMutex
	knownInstanceTypesList   []*string
)

type ASGDiscoverer struct {
	*fetcher.DataManager
	session *session.Session
	svc     *autoscaling.AutoScaling
	ec2svc  *ec2.EC2

	asgToInstanceTypeAndAZ map[string]string
	instanceTypeAndAZToAsg map[string]string
	autoDiscoveryTags      map[string]string

	launchConfigurationInstanceTypeCache map[string]utils.InstanceDetails
	launchTemplateInstanceTypeCache      map[string]utils.InstanceDetails
}

var _ fetcher.Fetcher = &ASGDiscoverer{}

func NewASGDiscoverer(refreshInterval time.Duration, autoDiscoveryTags map[string]string) (*ASGDiscoverer, error) {
	sess := session.New(aws.NewConfig().WithCredentialsChainVerboseErrors(true))
	asgDiscoverer := &ASGDiscoverer{
		session:                              sess,
		svc:                                  autoscaling.New(sess),
		ec2svc:                               ec2.New(sess),
		autoDiscoveryTags:                    autoDiscoveryTags,
		launchConfigurationInstanceTypeCache: make(map[string]utils.InstanceDetails),
		launchTemplateInstanceTypeCache:      make(map[string]utils.InstanceDetails),
	}
	asgDiscoverer.DataManager = fetcher.NewDataManager(asgDiscoverer, "ASG Fetcher", refreshInterval)
	if asgDiscoverer.DataManager == nil {
		return nil, fmt.Errorf("NewDataManager failed")
	}

	return asgDiscoverer, nil
}

func (asgd *ASGDiscoverer) getASGsByTags() (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	// this is copied from https://github.com/kubernetes/autoscaler/blob/e81674010e4545b980bd1f4808f0ae2ccf23c9af/cluster-autoscaler/cloudprovider/aws/auto_scaling.go#L214
	filters := []*autoscaling.Filter{}
	for key, value := range asgd.autoDiscoveryTags {
		filter := &autoscaling.Filter{
			Name:   aws.String("key"),
			Values: []*string{aws.String(key)},
		}
		filters = append(filters, filter)
		if value != "" {
			filters = append(filters, &autoscaling.Filter{
				Name:   aws.String("value"),
				Values: []*string{aws.String(value)},
			})
		}
	}

	tags := []*autoscaling.TagDescription{}
	res := &autoscaling.DescribeAutoScalingGroupsOutput{}

	if err := asgd.svc.DescribeTagsPages(&autoscaling.DescribeTagsInput{
		Filters:    filters,
		MaxRecords: aws.Int64(100),
	}, func(out *autoscaling.DescribeTagsOutput, _ bool) bool {
		tags = append(tags, out.Tags...)
		// We return true while we want to be called with the next page of
		// results, if any.
		return true
	}); err != nil {
		return res, err
	}

	asgNames := []string{}
	asgNameOccurrences := make(map[string]int)
	for _, t := range tags {
		asgName := aws.StringValue(t.ResourceId)
		occurrences := asgNameOccurrences[asgName] + 1
		if occurrences >= len(asgd.autoDiscoveryTags) {
			asgNames = append(asgNames, asgName)
		}
		asgNameOccurrences[asgName] = occurrences
	}

	tot := len(asgNames)
	if tot == 0 {
		return res, nil
	}

	// copied from https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/aws/auto_scaling.go#L147
	// AWS only accepts up to 50 ASG names as input, describe them in batches
	for i := 0; i < len(asgNames); i += 50 {
		end := i + 50

		if end > tot {
			end = tot
		}

		if err := asgd.svc.DescribeAutoScalingGroupsPages(&autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: aws.StringSlice(asgNames[i:end]),
			MaxRecords:            aws.Int64(100),
		}, func(out *autoscaling.DescribeAutoScalingGroupsOutput, _ bool) bool {
			res.AutoScalingGroups = append(res.AutoScalingGroups, out.AutoScalingGroups...)
			// We return true while we want to be called with the next page of
			// results, if any.
			return true
		}); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (asgd *ASGDiscoverer) getInstanceTypeByLCName(name string) (utils.InstanceDetails, error) {
	if instanceDetails, found := asgd.launchConfigurationInstanceTypeCache[name]; found {
		return instanceDetails, nil
	}

	params := &autoscaling.DescribeLaunchConfigurationsInput{
		LaunchConfigurationNames: []*string{aws.String(name)},
		MaxRecords:               aws.Int64(1),
	}
	launchConfigurations, err := asgd.svc.DescribeLaunchConfigurations(params)
	if err != nil {
		klog.V(4).Infof("Failed LaunchConfiguration info request for %s: %v", name, err)
		return utils.InstanceDetails{}, err
	}
	if len(launchConfigurations.LaunchConfigurations) < 1 {
		return utils.InstanceDetails{}, fmt.Errorf("unable to get first LaunchConfiguration for %s", name)
	}

	instanceType := *launchConfigurations.LaunchConfigurations[0].InstanceType
	iDetails := utils.InstanceDetails{
		InstanceType: instanceType,
		IsSpot:       (launchConfigurations.LaunchConfigurations[0].SpotPrice != nil),
	}
	asgd.launchConfigurationInstanceTypeCache[name] = iDetails

	return iDetails, nil
}

type launchTemplate struct {
	name    string
	version string
}

func (asgd *ASGDiscoverer) getInstanceTypeByLT(launchTemplate *launchTemplate) (utils.InstanceDetails, error) {
	ltCacheKey := fmt.Sprintf("%s---%s", launchTemplate.name, launchTemplate.version)
	if iDetails, found := asgd.launchTemplateInstanceTypeCache[ltCacheKey]; found {
		return iDetails, nil
	}

	params := &ec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateName: aws.String(launchTemplate.name),
		Versions:           []*string{aws.String(launchTemplate.version)},
	}

	describeData, err := asgd.ec2svc.DescribeLaunchTemplateVersions(params)
	if err != nil {
		return utils.InstanceDetails{}, err
	}

	if len(describeData.LaunchTemplateVersions) == 0 {
		return utils.InstanceDetails{}, fmt.Errorf("unable to find template versions")
	}

	lt := describeData.LaunchTemplateVersions[0]
	instanceType := lt.LaunchTemplateData.InstanceType
	isSpot := aws.StringValue(lt.LaunchTemplateData.InstanceMarketOptions.MarketType) == ec2.MarketTypeSpot

	if instanceType == nil {
		return utils.InstanceDetails{}, fmt.Errorf("unable to find instance type within launch template")
	}

	return utils.InstanceDetails{InstanceType: aws.StringValue(instanceType), IsSpot: isSpot}, nil
}

func (asgd *ASGDiscoverer) GetData() (interface{}, error) {
	return asgd.getASGsByTags()
}

func (asgd *ASGDiscoverer) ProcessData(data interface{}) error {
	r := data.(*autoscaling.DescribeAutoScalingGroupsOutput)

	asgToInstanceTypeAndAZ := make(map[string]string)
	instanceTypeAndAZToAsg := make(map[string]string)

	instanceTypesList := []*string{}
	instanceTypesMap := make(map[string]struct{})

	for _, asg := range r.AutoScalingGroups {
		var err error
		var asgName, az string
		var iDetails utils.InstanceDetails
		if len(asg.AvailabilityZones) > 0 {
			az = aws.StringValue(asg.AvailabilityZones[0])
		}

		if aws.StringValue(asg.LaunchConfigurationName) != "" {
			lcName := aws.StringValue(asg.LaunchConfigurationName)
			if iDetails, err = asgd.getInstanceTypeByLCName(lcName); err != nil {
				err = fmt.Errorf("Error getting instance type from LC: %s, %v", lcName, err)
				klog.Errorf(err.Error())
				return err
			}
		} else if asg.LaunchTemplate != nil {
			var version string
			if asg.LaunchTemplate.Version == nil {
				version = "$Default"
			} else {
				version = aws.StringValue(asg.LaunchTemplate.Version)
			}
			lt := &launchTemplate{
				name:    aws.StringValue(asg.LaunchTemplate.LaunchTemplateName),
				version: version,
			}
			if iDetails, err = asgd.getInstanceTypeByLT(lt); err != nil {
				err = fmt.Errorf("Error getting instance type from LT: %s, %v",
					fmt.Sprintf("%s (v %s)", lt.name, lt.version), err)
				klog.Errorf(err.Error())
				return err
			}
		}

		// fill caches
		asgName = aws.StringValue(asg.AutoScalingGroupName)
		iDetails.AvailabilityZone = az
		asgToInstanceTypeAndAZ[asgName] = iDetails.String()
		instanceTypeAndAZToAsg[iDetails.String()] = asgName
		instanceTypesMap[iDetails.InstanceType] = struct{}{}
	}

	asgd.asgToInstanceTypeAndAZ = asgToInstanceTypeAndAZ
	asgd.instanceTypeAndAZToAsg = instanceTypeAndAZToAsg

	for itype, _ := range instanceTypesMap {
		instanceTypesList = append(instanceTypesList, aws.String(itype))
	}
	knownInstanceTypesListMu.Lock()
	defer knownInstanceTypesListMu.Unlock()
	knownInstanceTypesList = instanceTypesList

	return nil
}

func (asgd *ASGDiscoverer) GetCheckSum(data interface{}) string {
	r := data.(*autoscaling.DescribeAutoScalingGroupsOutput)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(r.GoString())))
}

func (asgd *ASGDiscoverer) GetLastChanges() time.Time {
	return asgd.DataManager.GetLastChanges()
}

func (asgd *ASGDiscoverer) GetASGsData() (map[string]string, error) {
	asgd.DataManager.RLock()
	defer asgd.DataManager.RUnlock()
	return asgd.asgToInstanceTypeAndAZ, nil
}

func (asgd *ASGDiscoverer) GetAsgFromInstanceDetails(details utils.InstanceDetails) (string, error) {
	asgd.DataManager.RLock()
	defer asgd.DataManager.RUnlock()
	if asgName, ok := asgd.instanceTypeAndAZToAsg[details.String()]; ok {
		return asgName, nil
	}
	return "", fmt.Errorf("ASG not found for %+v", details)
}

func (asgd *ASGDiscoverer) GetInstanceDetailsFor(asgName string) (utils.InstanceDetails, error) {
	asgd.DataManager.RLock()
	defer asgd.DataManager.RUnlock()
	iDetails := utils.InstanceDetails{}
	if res, ok := asgd.asgToInstanceTypeAndAZ[asgName]; ok {
		(&iDetails).FromString(res)
		return iDetails, nil
	}
	return iDetails, fmt.Errorf("No instance details found for %s", asgName)
}

func (asgd *ASGDiscoverer) GetAsgFor(instanceType, az string, isSpot bool) (string, error) {
	if asgName, err := asgd.GetAsgFromInstanceDetails(utils.InstanceDetails{instanceType, az, isSpot}); err == nil {
		return asgName, nil
	}
	return "", fmt.Errorf("ASG not found for %s in %s (spot? %t)", instanceType, az, isSpot)
}

func (asgd *ASGDiscoverer) GetInstanceTypeAndAZFor(asgName string) ([]string, error) {
	if iDetails, err := asgd.GetInstanceDetailsFor(asgName); err == nil {
		return []string{iDetails.InstanceType, iDetails.AvailabilityZone}, nil

	}
	return []string{}, fmt.Errorf("No instance type and AZ found for %s", asgName)
}
