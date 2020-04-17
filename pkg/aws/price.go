package aws

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"k8s.io/klog"

	ec2instancesinfo "github.com/cristim/ec2-instances-info"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/fetcher"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/utils"
)

type Pricer struct {
	*fetcher.DataManager
	session *session.Session
	svc     *ec2.EC2

	instanceTypeAndAZToPrice     map[string]float64
	instanceTypeAndRegionToPrice map[string]float64
}

var _ fetcher.Fetcher = &Pricer{}

func NewPricer(refreshInterval time.Duration) (*Pricer, error) {
	sess := session.New(aws.NewConfig().WithCredentialsChainVerboseErrors(true))
	pricer := &Pricer{
		session: sess,
		svc:     ec2.New(sess),
	}
	pricer.DataManager = fetcher.NewDataManager(pricer, "Prices Fetcher", refreshInterval)

	if pricer.DataManager == nil {
		return nil, fmt.Errorf("NewDataManager failed")
	}

	return pricer, nil
}

type pricesData struct {
	spotPrices     *ec2.DescribeSpotPriceHistoryOutput
	ondemandPrices *ec2instancesinfo.InstanceData
}

func (p *Pricer) GetData() (interface{}, error) {
	knownInstanceTypesListMu.Lock()
	instanceTypesList := knownInstanceTypesList
	knownInstanceTypesListMu.Unlock()

	c := make(chan *ec2instancesinfo.InstanceData)
	go func() {
		data, err := ec2instancesinfo.Data()
		if err != nil {
			klog.Errorf(err.Error())
		}
		c <- data
	}()

	res := &pricesData{
		spotPrices: &ec2.DescribeSpotPriceHistoryOutput{},
	}
	if err := p.svc.DescribeSpotPriceHistoryPages(
		&ec2.DescribeSpotPriceHistoryInput{
			ProductDescriptions: []*string{
				aws.String("Linux/UNIX"),
			},
			StartTime:     aws.Time(time.Now()),
			EndTime:       aws.Time(time.Now()),
			InstanceTypes: instanceTypesList,
		},
		func(page *ec2.DescribeSpotPriceHistoryOutput, lastPage bool) bool {
			res.spotPrices.SpotPriceHistory = append(res.spotPrices.SpotPriceHistory, page.SpotPriceHistory...)
			return true
		}); err != nil {
		return nil, err
	}

	res.ondemandPrices = <-c

	return res, nil
}

func (p *Pricer) ProcessData(data interface{}) error {
	r := data.(*pricesData)

	instanceTypeAndAZToPrice := make(map[string]float64)
	instanceTypeAndRegionToPrice := make(map[string]float64)

	for _, priceInfo := range r.spotPrices.SpotPriceHistory {
		iDetails := utils.InstanceDetails{
			InstanceType:     aws.StringValue(priceInfo.InstanceType),
			AvailabilityZone: aws.StringValue(priceInfo.AvailabilityZone),
			IsSpot:           true,
		}
		if price, err := strconv.ParseFloat(aws.StringValue(priceInfo.SpotPrice), 64); err == nil {
			instanceTypeAndAZToPrice[iDetails.String()] = price
		} else {
			klog.Errorf(err.Error())
			return err
		}
	}

	p.instanceTypeAndAZToPrice = instanceTypeAndAZToPrice

	if r.ondemandPrices != nil {
		iDetails := utils.InstanceDetails{
			AvailabilityZone: fmt.Sprintf("%sX", aws.StringValue(p.session.Config.Region)),
			IsSpot:           false,
		}
		for _, it := range *r.ondemandPrices {
			iDetails.InstanceType = it.InstanceType
			instanceTypeAndRegionToPrice[iDetails.String()] = it.Pricing[*p.session.Config.Region].Linux.OnDemand
		}
	}

	p.instanceTypeAndRegionToPrice = instanceTypeAndRegionToPrice

	return nil
}

func (p *Pricer) GetCheckSum(data interface{}) string {
	r := data.(*pricesData)
	checksum1 := fmt.Sprintf("%x", sha256.Sum256([]byte(r.spotPrices.GoString())))
	checksum2 := ""
	if r.ondemandPrices != nil {
		if jsonData, err := json.Marshal(*r.ondemandPrices); err == nil {
			checksum2 = fmt.Sprintf("%x", sha256.Sum256(jsonData))
		}
	}
	return fmt.Sprintf("%s%s", checksum1, checksum2)
}

func (p *Pricer) GetPriceFor(instanceType, az string, isSpot bool) (float64, bool) {
	p.DataManager.RLock()
	defer p.DataManager.RUnlock()
	if isSpot {
		iDetails := utils.InstanceDetails{
			InstanceType:     instanceType,
			AvailabilityZone: az,
			IsSpot:           true,
		}
		price, found := p.instanceTypeAndAZToPrice[iDetails.String()]
		return price, found
	} else {
		iDetails := utils.InstanceDetails{
			InstanceType:     instanceType,
			AvailabilityZone: fmt.Sprintf("%sX", az[:len(az)-1]),
			IsSpot:           false,
		}
		price, found := p.instanceTypeAndRegionToPrice[iDetails.String()]
		return price, found
	}

}
