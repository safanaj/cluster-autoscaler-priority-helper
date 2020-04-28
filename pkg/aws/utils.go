package aws

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go/aws"
)

const METADATA_AZ_URL = "http://169.254.169.254/latest/meta-data/placement/availability-zone"

func detectCurrentRegion() (string, error) {
	var (
		err  error
		body []byte
		resp *http.Response
	)

	if resp, err = http.Get(url); err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if body, err = ioutil.ReadAll(resp.Body); err != nil {
		return nil, err
	}
	if len(body) < 2 {
		return nil, fmt.Errorf("Invalid AZ detected: %v", body)
	}
	return string(body[:len(body)-1]), nil
}

func getAwsConfig() *aws.Config {
	awsCfg := aws.NewConfig().WithCredentialsChainVerboseErrors(true)
	if awsCfg.Region == nil {
		if _, ok := os.LookupEnv("AWS_REGION"); !ok {
			if region, err := detectCurrentRegion(); err == nil {
				awsCfg = awsCfg.WithRegion(region)
			} else {
				panic(err)
			}
		}
	}
	return awsCfg
}
