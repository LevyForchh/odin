package models

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/autoscaling/autoscalingiface"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/coinbase/odin/aws"
	"github.com/coinbase/odin/aws/alb"
	"github.com/coinbase/odin/aws/asg"
	"github.com/coinbase/odin/aws/elb"
	"github.com/coinbase/odin/aws/iam"
	"github.com/coinbase/odin/aws/lc"
	"github.com/coinbase/odin/aws/pg"
	"github.com/coinbase/odin/aws/sg"
	"github.com/coinbase/step/utils/is"
	"github.com/coinbase/step/utils/to"
)

// HealthReport is built to make log lines like:
// web: .....|.
// gray targets, red terminated, yellow unhealthy, green healthy
type HealthReport struct {
	TargetHealthy  *int64   `json:"target_healthy,omitempty"`  // Number of instances aimed to to Launch
	TargetLaunched *int64   `json:"target_launched,omitempty"` // Number of instances aimed to to Launch
	Healthy        *int     `json:"healthy,omitempty"`         // Number of instances that are healthy
	Launching      *int     `json:"launching,omitempty"`       // Number of instances that have been created
	Terminating    *int     `json:"terminating,omitempty"`     // Number of instances that are Terminating
	TerminatingIDs []string `json:"terminating_ids,omitempty"` // Instance IDs that are Terminating

	DesiredCapacity *int64 `json:"desired_capacity,omitempty"` // The current desired capacity goal
	MinSize         *int64 `json:"min_size,omitempty"`         // The current min size
}

// TYPES

// Service struct
type Service struct {
	release  *Release
	userdata *string

	// Generated
	ServiceName *string `json:"service_name,omitempty"`

	// Find these Resources
	ELBs           []*string          `json:"elbs,omitempty"`
	Profile        *string            `json:"profile,omitempty"`
	TargetGroups   []*string          `json:"target_groups,omitempty"`
	SecurityGroups []*string          `json:"security_groups,omitempty"`
	Tags           map[string]*string `json:"tags,omitempty"`

	// Create Resources
	InstanceType *string            `json:"instance_type,omitempty"`
	Autoscaling  *AutoScalingConfig `json:"autoscaling,omitempty"`
	SpotPrice    *string            `json:"spot_price,omitempty"`

	// Strategy contains all the information about how to scale
	strategy *Strategy

	// EBS
	EBSVolumeSize *int64  `json:"ebs_volume_size,omitempty"`
	EBSVolumeType *string `json:"ebs_volume_type,omitempty"`
	EBSDeviceName *string `json:"ebs_device_name,omitempty"`

	// Placement Group
	PlacementGroupName           *string `json:"placement_group_name,omitempty"`
	PlacementGroupPartitionCount *int64  `json:"placement_group_partition_count,omitempty"`
	PlacementGroupStrategy       *string `json:"placement_group_strategy,omitempty"`

	// Network
	AssociatePublicIpAddress *bool `json:"associate_public_ip_address,omitempty"`

	// Found Resources
	Resources *ServiceResourceNames `json:"resources,omitempty"`

	// Created Resources
	CreatedASG              *string `json:"created_asg,omitempty"`
	PreviousDesiredCapacity *int64  `json:"previous_desired_capacity,omitempty"`

	// What is Healthy
	HealthReport *HealthReport `json:"healthy_report,omitempty"`
	Healthy      bool

	PauseForSlowStart *int64 `json:"pause_for_slow_start"`
}

//////////
// Getters
//////////

// ProjectName returns project name
func (service *Service) ProjectName() *string {
	return service.release.ProjectName
}

// ConfigName returns config name
func (service *Service) ConfigName() *string {
	return service.release.ConfigName
}

// Name service name
func (service *Service) Name() *string {
	return service.ServiceName
}

// ReleaseUUID returns release UUID
func (service *Service) ReleaseUUID() *string {
	return service.release.UUID
}

// ReleaseID returns release ID
func (service *Service) ReleaseID() *string {
	return service.release.ReleaseID
}

// CreatedAt returns created at data
func (service *Service) CreatedAt() *time.Time {
	return service.release.CreatedAt
}

// ServiceID returns a formatted string of the services ID
func (service *Service) ServiceID() *string {
	if service.ProjectName() == nil || service.ConfigName() == nil || service.ServiceName == nil || service.CreatedAt() == nil {
		return nil
	}

	tf := strings.Replace(service.CreatedAt().UTC().Format(time.RFC3339), ":", "-", -1)
	return to.Strp(fmt.Sprintf("%v-%v-%v-%v", *service.ProjectName(), *service.ConfigName(), tf, *service.ServiceName))
}

// Subnets returns subnets
func (service *Service) Subnets() []*string {
	return service.release.Subnets
}

// UserData will take the releases template and override
func (service *Service) UserData() *string {
	templateARGs := []string{}
	templateARGs = append(templateARGs, "{{RELEASE_ID}}", to.Strs(service.ReleaseID()))
	templateARGs = append(templateARGs, "{{PROJECT_NAME}}", to.Strs(service.ProjectName()))
	templateARGs = append(templateARGs, "{{CONFIG_NAME}}", to.Strs(service.ConfigName()))
	templateARGs = append(templateARGs, "{{SERVICE_NAME}}", to.Strs(service.ServiceName))

	templateARGs = append(templateARGs, "{{RELEASE_BUCKET}}", to.Strs(service.release.Bucket))
	templateARGs = append(templateARGs, "{{AWS_ACCOUNT_ID}}", to.Strs(service.release.AwsAccountID))
	templateARGs = append(templateARGs, "{{AWS_REGION}}", to.Strs(service.release.AwsRegion))

	templateARGs = append(templateARGs, "{{SHARED_PROJECT_DIR}}", to.Strs(service.release.SharedProjectDir()))
	templateARGs = append(templateARGs, "{{RELEASE_DIR}}", to.Strs(service.release.ReleaseDir()))

	replacer := strings.NewReplacer(templateARGs...)

	return to.Strp(replacer.Replace(to.Strs(service.userdata)))
}

// SetUserData sets the userdata
func (service *Service) SetUserData(userdata *string) {
	service.userdata = userdata
}

// LifeCycleHooks returns
func (service *Service) LifeCycleHooks() map[string]*LifeCycleHook {
	return service.release.LifeCycleHooks
}

// SubnetIds returns
func (service *Service) SubnetIds() *string {
	return to.Strp(strings.Join(to.StrSlice(service.Resources.Subnets), ","))
}

// LifeCycleHookSpecs returns
func (service *Service) LifeCycleHookSpecs() []*autoscaling.LifecycleHookSpecification {
	lcs := []*autoscaling.LifecycleHookSpecification{}
	for _, lc := range service.LifeCycleHooks() {
		lcs = append(lcs, lc.ToLifecycleHookSpecification())
	}
	return lcs
}

func (service *Service) errorPrefix() string {
	if service.ServiceName == nil {
		return fmt.Sprintf("Service Error:")
	}
	return fmt.Sprintf("Service(%v) Error:", *service.ServiceName)
}

//////////
// Setters
//////////

// SetDefaults assigns default values
func (service *Service) SetDefaults(release *Release, serviceName string) {
	service.release = release

	service.ServiceName = &serviceName

	// Autoscaling Defaults
	if service.Autoscaling == nil {
		service.Autoscaling = &AutoScalingConfig{}
	}

	if service.Resources == nil {
		service.Resources = &ServiceResourceNames{
			Subnets: []*string{to.Strp("place_holder")},
		}
	}

	service.Autoscaling.SetDefaults(service.ServiceID(), service.release.Timeout)

	service.strategy = &Strategy{service.Autoscaling, service.PreviousDesiredCapacity}
}

// setHealthy sets the health state from the instances
func (service *Service) setHealthy(group *asg.ASG, instances aws.Instances) {
	healthy := instances.HealthyIDs()
	terming := instances.TerminatingIDs()

	service.HealthReport = &HealthReport{
		TargetHealthy:  to.Int64p(service.strategy.TargetHealthy()),
		TargetLaunched: to.Int64p(service.strategy.TargetCapacity()),
		Healthy:        to.Intp(len(healthy)),
		Terminating:    to.Intp(len(terming)),
		TerminatingIDs: terming,
		Launching:      to.Intp(len(instances)),

		DesiredCapacity: group.DesiredCapacity,
		MinSize:         group.MinSize,
	}

	// The Service is Healthy if
	// the number of instances that are healthy is greater than or equal to the target
	service.Healthy = int64(len(healthy)) >= service.strategy.TargetHealthy()
}

//////////
// Validate
//////////

// Validate validates the service
func (service *Service) Validate() error {
	if err := service.ValidateAttributes(); err != nil {
		return fmt.Errorf("%v %v", service.errorPrefix(), err.Error())
	}

	for name, lc := range service.LifeCycleHooks() {
		if lc == nil {
			return fmt.Errorf("LifeCycle %v is nil", name)
		}

		err := lc.ValidateAttributes()
		if err != nil {
			return err
		}
	}

	// VALIDATE Autoscaling Group Input (this in implemented by AWS)
	if err := service.createInput().Validate(); err != nil {
		return fmt.Errorf("%v %v", service.errorPrefix(), err.Error())
	}

	if err := service.createLaunchConfigurationInput().Validate(); err != nil {
		return fmt.Errorf("%v %v", service.errorPrefix(), err.Error())
	}

	return nil
}

// ValidateAttributes validates attributes
func (service *Service) ValidateAttributes() error {
	if is.EmptyStr(service.ServiceName) {
		return fmt.Errorf("ServiceName must be defined")
	}

	if is.EmptyStr(service.InstanceType) {
		return fmt.Errorf("InstanceType must be defined")
	}

	if service.Autoscaling == nil {
		return fmt.Errorf("Autoscaling must be defined")
	}

	if err := service.Autoscaling.ValidateAttributes(); err != nil {
		return err
	}

	// Must have security groups
	if len(service.SecurityGroups) < 1 {
		return fmt.Errorf("Security Groups must be included")
	}

	if !is.UniqueStrp(service.SecurityGroups) {
		return fmt.Errorf("Security Group must be unique")
	}

	if !is.UniqueStrp(service.ELBs) {
		// Non unique string in ELBs or nil value
		return fmt.Errorf("Non Unique ELBs")
	}

	if !is.UniqueStrp(service.TargetGroups) {
		// Non unique string in ELBs or nil value
		return fmt.Errorf("Non Unique TargetGroups")
	}

	if err := service.validatePlacementGroupAttributes(); err != nil {
		return err
	}

	return nil
}

func (service *Service) validatePlacementGroupAttributes() error {
	// if PlacementGroupName is not nil, then there must be a Strategy either cluster | spread | partition
	// if the strategy is partition then there must be a partition count
	if service.PlacementGroupName != nil {
		if service.PlacementGroupStrategy == nil {
			return fmt.Errorf("PlacementGroupStrategy must be defined with PlacementGroupName")
		}

		strat := *service.PlacementGroupStrategy

		// must be one of cluster | spread | partition
		if strat != "cluster" && strat != "spread" && strat != "partition" {
			return fmt.Errorf("PlacementGroupStrategy must be one of 'cluster' or 'spread' or 'partition'")
		}

		if strat == "partition" && service.PlacementGroupPartitionCount == nil {
			return fmt.Errorf("PlacementGroupPartitionCount must be defined if PlacementGroupName and strategy is 'partition'")
		}

		if strat != "partition" && service.PlacementGroupPartitionCount != nil {
			return fmt.Errorf("PlacementGroupPartitionCount only valid for 'partition' strategy")
		}
	}

	return nil
}

//////////
// Validate Resources
//////////

// FetchResources attempts to retrieve all resources
func (service *Service) FetchResources(ec2 aws.EC2API, elbc aws.ELBAPI, albc aws.ALBAPI, iamc aws.IAMAPI) (*ServiceResources, error) {
	// RESOURCES THAT ARE PROJECT-CONFIG-SERVICE specific
	// Fetch Security Group
	sgs, err := sg.Find(ec2, service.SecurityGroups)
	if err != nil {
		return nil, err
	}

	// FETCH ELBS
	elbs, err := elb.FindAll(elbc, service.ELBs)
	if err != nil {
		return nil, err
	}

	// Fetch TargetGroups
	targetGroups, err := alb.FindAll(albc, service.TargetGroups)
	if err != nil {
		return nil, err
	}

	if service.PlacementGroupName != nil {
		if err := pg.FindOrCreatePartitionGroup(
			ec2,
			fmt.Sprintf("odin/%s/%s", *service.ProjectName(), *service.ConfigName()), // only required if new placement group is created
			service.PlacementGroupName,
			service.PlacementGroupPartitionCount,
			service.PlacementGroupStrategy,
		); err != nil {
			return nil, err
		}
	}

	// FETCH IAM
	var iamProfile *iam.Profile
	if service.Profile != nil {
		iamProfile, err = iam.Find(iamc, service.Profile)
		if err != nil {
			return nil, err
		}
	}

	return &ServiceResources{
		SecurityGroups: sgs,
		ELBs:           elbs,
		TargetGroups:   targetGroups,
		Profile:        iamProfile,
	}, nil
}

//////////
// Create Resources
//////////

// CreateResources creates the ASG and Launch configuration for the service
func (service *Service) CreateResources(asgc aws.ASGAPI, cwc aws.CWAPI) error {

	err := service.createLaunchConfiguration(asgc)
	if err != nil {
		return err
	}

	createdASG, err := service.createASG(asgc)

	if err != nil {
		return err
	}

	service.CreatedASG = createdASG.AutoScalingGroupName

	if err := service.createAutoScalingPolicies(asgc, cwc); err != nil {
		return err
	}

	service.setHealthy(createdASG, aws.Instances{})

	if err := service.createMetricsCollection(asgc); err != nil {
		return err
	}

	return nil
}

func (service *Service) createInput() *asg.Input {
	input := &asg.Input{&autoscaling.CreateAutoScalingGroupInput{}}

	input.AutoScalingGroupName = service.ServiceID()
	input.LaunchConfigurationName = service.ServiceID()

	// Adjusted by strategy
	input.MinSize = service.strategy.InitialMinSize()
	input.DesiredCapacity = service.strategy.InitialDesiredCapacity()

	// Unchanging values from AutoScalingConfig
	input.MaxSize = service.Autoscaling.MaxSize
	input.DefaultCooldown = service.Autoscaling.DefaultCooldown
	input.HealthCheckGracePeriod = service.Autoscaling.HealthCheckGracePeriod

	input.LoadBalancerNames = service.Resources.ELBs
	input.TargetGroupARNs = service.Resources.TargetGroups

	input.VPCZoneIdentifier = service.SubnetIds()
	input.LifecycleHookSpecificationList = service.LifeCycleHookSpecs()

	if service.PlacementGroupName != nil {
		input.PlacementGroup = service.PlacementGroupName
	}

	for key, value := range service.Tags {
		input.AddTag(key, value)
	}

	input.AddTag("ProjectName", service.ProjectName())
	input.AddTag("ConfigName", service.ConfigName())
	input.AddTag("ServiceName", service.ServiceName)
	input.AddTag("ReleaseID", service.ReleaseID())
	input.AddTag("ReleaseUUID", service.ReleaseUUID())
	input.AddTag("Name", service.ServiceID())

	input.SetDefaults()

	return input
}

func (service *Service) createAutoScalingPolicies(asgc aws.ASGAPI, cwc aws.CWAPI) error {
	for _, policy := range service.Autoscaling.Policies {
		if err := policy.Create(asgc, cwc, service.ServiceID()); err != nil {
			return err
		}
	}

	return nil
}

func (service *Service) createASG(asgc aws.ASGAPI) (*asg.ASG, error) {
	input := service.createInput()

	if err := input.Create(asgc); err != nil {
		return nil, err
	}

	return input.ToASG(), nil
}

func (service *Service) createLaunchConfigurationInput() *lc.LaunchConfigInput {
	input := &lc.LaunchConfigInput{&autoscaling.CreateLaunchConfigurationInput{}}
	input.SetDefaults()

	input.LaunchConfigurationName = service.ServiceID()

	if service.Resources != nil {
		input.ImageId = service.Resources.Image
		input.SecurityGroups = service.Resources.SecurityGroups
		input.IamInstanceProfile = service.Resources.Profile
	}
	input.InstanceType = service.InstanceType

	input.AssociatePublicIpAddress = service.AssociatePublicIpAddress

	input.UserData = to.Base64p(service.UserData())

	input.AddBlockDevice(service.EBSVolumeSize, service.EBSVolumeType, service.EBSDeviceName)

	input.SpotPrice = service.SpotPrice

	return input
}

func (service *Service) createLaunchConfiguration(asgc autoscalingiface.AutoScalingAPI) error {
	input := service.createLaunchConfigurationInput()

	if err := input.Create(asgc); err != nil {
		return err
	}

	return nil
}

func (service *Service) createMetricsCollection(asgc aws.ASGAPI) error {
	// Ref: https://docs.aws.amazon.com/sdk-for-go/api/service/autoscaling/#EnableMetricsCollectionInput
	// If you omit this parameter (`Metrics`), all metrics are enabled which is desired.
	metricInput := &autoscaling.EnableMetricsCollectionInput{
		AutoScalingGroupName: service.CreatedASG,
		Granularity:          to.Strp("1Minute"),
	}

	_, err := asgc.EnableMetricsCollection(metricInput)

	if err != nil {
		return err
	}

	return nil
}

//////////
// Healthy Resources
//////////

// HaltError error
type HaltError struct {
	err error
}

// Error returns error
func (he *HaltError) Error() string {
	return he.err.Error()
}

// UpdateHealthy updates the health status of the service
// This might cause a Halt Error which will force the release to stop
func (service *Service) UpdateHealthy(asgc aws.ASGAPI, elbc aws.ELBAPI, albc aws.ALBAPI) error {
	all, group, err := asg.GetInstances(asgc, service.CreatedASG)
	if err != nil {
		return err // This might retry
	}

	// Early exit and Halt if there are instances Terminating
	if service.strategy.ReachedMaxTerminations(all) {
		err := fmt.Errorf("Found terming instances %v, %v", *service.ServiceName, strings.Join(all.TerminatingIDs(), ","))
		return &HaltError{err} // This will immediately stop deploying
	}

	// Fetch All the instances
	for _, checkELB := range service.Resources.ELBs {
		elbInstances, err := elb.GetInstances(elbc, checkELB, all.InstanceIDs())
		if err != nil {
			return err // This might retry
		}

		all = all.MergeInstances(elbInstances)
	}

	for _, checkTG := range service.Resources.TargetGroups {
		tgInstances, err := alb.GetInstances(albc, checkTG, all.InstanceIDs())

		if err != nil {
			return err // This might retry
		}

		all = all.MergeInstances(tgInstances)
	}

	// Set the Healthy Value
	service.setHealthy(group, all) // TODO: maybe use the new min and dc

	// Use the strategy to calculate the new values of min_size and desired_capacity
	min, dc := service.strategy.CalculateMinDesired(all)

	if err := service.SafeSetMinDesiredCapacity(asgc, group, min, dc); err != nil {
		return fmt.Errorf("Setting Min and Desired Capacity Error for %v: %v", *service.ServiceName, err.Error())
	}

	return nil
}

func (service *Service) SlowStartDuration(albc aws.ALBAPI) int {
	longest := 0
	for _, arn := range service.Resources.TargetGroups {
		input := elbv2.DescribeTargetGroupAttributesInput{TargetGroupArn: arn}
		output, _ := albc.DescribeTargetGroupAttributes(&input)
		for _, attribute := range output.Attributes {
			if *attribute.Key == "slow_start.duration_seconds" {
				d, _ := strconv.Atoi(*attribute.Value)
				if d > longest {
					longest = d
				}
			}
		}
	}
	return longest
}

//////////
// Update Resources
//////////

// SafeSetMinDesiredCapacity is a wrapper around SetMinDesiredCapacity which ensures that
// 1. minSize and desiredCapacity are never lower than the existing group
// 2. minSize is never higher than desiredCapacity
// 3. we don't do anything if the values dont change
func (service *Service) SafeSetMinDesiredCapacity(asgc aws.ASGAPI, group *asg.ASG, minSize, desiredCapacity int64) error {

	// Ensure that minSize and Desired capacity are never lower than the current group
	// This might happen in long deploys if instances disappear
	minSize = max(minSize, *group.MinSize)
	desiredCapacity = max(desiredCapacity, *group.DesiredCapacity)

	// Min should always be lower than DC
	minSize = min(minSize, desiredCapacity)

	if minSize == *group.MinSize && desiredCapacity == *group.DesiredCapacity {
		// If minSize and desired don't change return nothing
		return nil
	}

	return service.SetMinDesiredCapacity(asgc, to.Int64p(minSize), to.Int64p(desiredCapacity))
}

func (service *Service) SetMinDesiredCapacity(asgc aws.ASGAPI, minSize, desiredCapacity *int64) error {
	_, err := asgc.UpdateAutoScalingGroup(&autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: service.CreatedASG,
		MinSize:              minSize,
		DesiredCapacity:      desiredCapacity,
	})

	return err
}

// ResetDesiredCapacity sets the min and desired capacities to their final values
func (service *Service) ResetDesiredCapacity(asgc aws.ASGAPI) error {
	return service.SetMinDesiredCapacity(
		asgc,
		service.Autoscaling.MinSize,
		to.Int64p(service.strategy.DesiredCapacity()),
	)
}
