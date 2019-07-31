package sqs

import (
	"fmt"
	"github.com/applike/gosoline/pkg/cfg"
	"github.com/applike/gosoline/pkg/encoding/json"
	"github.com/applike/gosoline/pkg/mon"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sqs/sqsiface"
	"strconv"
	"sync"
)

const DefaultVisibilityTimeout = "30"

type CreateQueueInput struct {
	Name              string
	RedrivePolicy     RedrivePolicy
	VisibilityTimeout int
}

type ServiceSettings struct {
	AutoCreate bool
}

type service struct {
	lck      sync.Mutex
	logger   mon.Logger
	client   sqsiface.SQSAPI
	settings *ServiceSettings
}

func NewService(config cfg.Config, logger mon.Logger) *service {
	client := GetClient(config, logger)
	settings := &ServiceSettings{
		AutoCreate: config.GetBool("aws_sqs_autoCreate"),
	}

	return NewServiceWithInterfaces(logger, client, settings)
}

func NewServiceWithInterfaces(logger mon.Logger, client sqsiface.SQSAPI, settings *ServiceSettings) *service {
	return &service{
		logger:   logger,
		client:   client,
		settings: settings,
	}
}

func (s service) CreateQueue(input *CreateQueueInput) (*Properties, error) {
	s.lck.Lock()
	defer s.lck.Unlock()

	exists, err := s.QueueExists(input.Name)

	if err != nil {
		return nil, err
	}

	if exists {
		return s.GetProperties(input.Name)
	}

	if !exists && !s.settings.AutoCreate {
		return nil, fmt.Errorf("sqs queue with name %s does not exist", input.Name)
	}

	attributes, err := s.createDeadLetterQueue(input.Name, input.RedrivePolicy)

	if err != nil {
		return nil, err
	}

	sqsInput := &sqs.CreateQueueInput{
		QueueName:  aws.String(input.Name),
		Attributes: make(map[string]*string),
	}

	for k, v := range attributes {
		sqsInput.Attributes[k] = v
	}

	props, err := s.doCreateQueue(sqsInput)

	if err != nil {
		return nil, err
	}

	visibilityTimeout := DefaultVisibilityTimeout
	if input.VisibilityTimeout > 0 {
		visibilityTimeout = strconv.Itoa(input.VisibilityTimeout)
	}

	_, err = s.client.SetQueueAttributes(&sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(props.Url),
		Attributes: map[string]*string{
			sqs.QueueAttributeNameVisibilityTimeout: aws.String(visibilityTimeout),
		},
	})

	return props, err
}

func (s service) createDeadLetterQueue(queueName string, redrivePolicy RedrivePolicy) (map[string]*string, error) {
	attributes := make(map[string]*string)

	if !redrivePolicy.Enabled {
		return attributes, nil
	}

	deadLetterName := fmt.Sprintf("%s-dead", queueName)
	deadLetterInput := &sqs.CreateQueueInput{
		QueueName: aws.String(deadLetterName),
	}

	props, err := s.doCreateQueue(deadLetterInput)

	if err != nil {
		s.logger.Errorf(err, "could not get arn of dead letter sqs queue %v", deadLetterName)
		return attributes, err
	}

	policy := map[string]string{
		"deadLetterTargetArn": props.Arn,
		"maxReceiveCount":     strconv.Itoa(redrivePolicy.MaxReceiveCount),
	}

	b, err := json.Marshal(policy)

	if err != nil {
		s.logger.Fatalf(err, "could not get marshal redrive policy for sqs queue %v", queueName)
		return attributes, err
	}

	attributes[sqs.QueueAttributeNameRedrivePolicy] = aws.String(string(b))

	return attributes, nil
}

func (s service) doCreateQueue(input *sqs.CreateQueueInput) (*Properties, error) {
	name := *input.QueueName

	s.logger.Infof("trying to create sqs queue: %v", name)
	_, err := s.client.CreateQueue(input)

	if err != nil {
		s.logger.Errorf(err, "could not create sqs queue %v", name)
		return nil, err
	}

	s.logger.Infof("created sqs queue %v", name)

	return s.GetProperties(name)
}

func (s service) QueueExists(name string) (bool, error) {
	s.logger.WithFields(mon.Fields{
		"name": name,
	}).Info("checking the existence of sqs queue")

	url, err := s.GetUrl(name)

	if err != nil {
		return false, err
	}

	if len(url) > 0 {
		s.logger.Infof("found queue %s with url %s", name, url)
		return true, nil
	}

	s.logger.WithFields(mon.Fields{
		"name": name,
	}).Info("could not found queue")

	return false, nil
}

func (s service) GetProperties(name string) (*Properties, error) {
	url, err := s.GetUrl(name)

	if err != nil {
		return nil, err
	}

	arn, err := s.GetArn(url)

	if err != nil {
		return nil, err
	}

	properties := &Properties{
		Name: name,
		Url:  url,
		Arn:  arn,
	}

	return properties, nil
}

func (s service) GetUrl(name string) (string, error) {
	input := &sqs.GetQueueUrlInput{
		QueueName: aws.String(name),
	}

	out, err := s.client.GetQueueUrl(input)

	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == sqs.ErrCodeQueueDoesNotExist {
			return "", nil
		}

		return "", err
	}

	return *out.QueueUrl, nil
}

func (s service) GetArn(url string) (string, error) {
	input := &sqs.GetQueueAttributesInput{
		AttributeNames: []*string{aws.String("QueueArn")},
		QueueUrl:       aws.String(url),
	}

	out, err := s.client.GetQueueAttributes(input)

	if err != nil {
		return "", err
	}

	arn := *(out.Attributes["QueueArn"])

	return arn, nil
}
