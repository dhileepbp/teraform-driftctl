package aws

import (
	"context"
	"fmt"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/providers/providerconfig"
)

// Provider implements AWS cloud resource fetching.
type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string { return "aws" }

func (p *Provider) SupportedTypes() []string {
	return []string{
		"aws_instance",
		"aws_s3_bucket",
		"aws_security_group",
		"aws_vpc",
		"aws_subnet",
	}
}

func (p *Provider) FetchResources(ctx context.Context, expected []model.Resource, opts providerconfig.FetchOptions) ([]model.Resource, error) {
	regionSet := make(map[string]bool)
	for _, r := range opts.Regions {
		regionSet[r] = true
	}
	for _, e := range expected {
		if e.Region != "" {
			regionSet[e.Region] = true
		}
	}
	if len(regionSet) == 0 {
		regionSet["us-east-1"] = true
	}

	byType := groupByType(expected)
	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		resources []model.Resource
		errs      []error
	)

	for region := range regionSet {
		region := region
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("region %s: %w", region, err))
				mu.Unlock()
				return
			}

			ec2Client := ec2.NewFromConfig(cfg)
			s3Client := s3.NewFromConfig(cfg)

			var regionResources []model.Resource
			var regionErrs []error

			if items := byType["aws_instance"]; len(items) > 0 {
				r, e := fetchInstances(ctx, ec2Client, region, items)
				regionResources = append(regionResources, r...)
				regionErrs = append(regionErrs, e...)
			}
			if items := byType["aws_vpc"]; len(items) > 0 {
				r, e := fetchVPCs(ctx, ec2Client, region, items)
				regionResources = append(regionResources, r...)
				regionErrs = append(regionErrs, e...)
			}
			if items := byType["aws_subnet"]; len(items) > 0 {
				r, e := fetchSubnets(ctx, ec2Client, region, items)
				regionResources = append(regionResources, r...)
				regionErrs = append(regionErrs, e...)
			}
			if items := byType["aws_security_group"]; len(items) > 0 {
				r, e := fetchSecurityGroups(ctx, ec2Client, region, items)
				regionResources = append(regionResources, r...)
				regionErrs = append(regionErrs, e...)
			}
			if items := byType["aws_s3_bucket"]; len(items) > 0 {
				r, e := fetchBuckets(ctx, s3Client, region, items)
				regionResources = append(regionResources, r...)
				regionErrs = append(regionErrs, e...)
			}

			mu.Lock()
			resources = append(resources, regionResources...)
			errs = append(errs, regionErrs...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	if len(errs) > 0 && len(resources) == 0 {
		return resources, fmt.Errorf("aws fetch failed: %v", errs[0])
	}

	return resources, nil
}

func groupByType(expected []model.Resource) map[string][]model.Resource {
	out := make(map[string][]model.Resource)
	for _, r := range expected {
		out[r.Type] = append(out[r.Type], r)
	}
	return out
}

func baseResource(resType, cloudID, name, region string, attrs map[string]any, tags map[string]string) model.Resource {
	return model.Resource{
		ID:         fmt.Sprintf("aws/%s/%s", resType, cloudID),
		Provider:   "aws",
		Type:       resType,
		CloudID:    cloudID,
		Name:       name,
		Attributes: attrs,
		Tags:       tags,
		Region:     region,
		Source:     "cloud",
	}
}
