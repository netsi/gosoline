package mdlsub

import (
	"context"
	"fmt"
	"github.com/applike/gosoline/pkg/cfg"
	"github.com/applike/gosoline/pkg/mdl"
	"github.com/applike/gosoline/pkg/mon"
	"time"
)

const (
	ConfigKeyMdlSubSubscribers = "mdlsub.subscribers"
	MetricNameSuccess          = "ModelEventConsumeSuccess"
	MetricNameFailure          = "ModelEventConsumeFailure"
)

type SubscriberSettings struct {
	Input       string      `cfg:"input" default:"sns"`
	Output      string      `cfg:"output"`
	RunnerCount int         `cfg:"runner_count" default:"10" validate:"min=1"`
	SourceModel mdl.ModelId `cfg:"source"`
	TargetModel mdl.ModelId `cfg:"target"`
}

type SubscriberModel struct {
	cfg.AppId
	Name string `cfg:"name"`
}

type SubscriberCallback struct {
	logger       mon.Logger
	metric       mon.MetricWriter
	application  string
	transformers ModelTransformers
	outputs      Outputs
}

func NewSubscriberCallback(transformers ModelTransformers, outputs Outputs) *SubscriberCallback {
	return &SubscriberCallback{
		transformers: transformers,
		outputs:      outputs,
	}
}

func (s *SubscriberCallback) Boot(config cfg.Config, logger mon.Logger) error {
	application := config.GetString("app_name")
	defaultMetrics := s.getDefaultMetrics(application)

	s.logger = logger
	s.metric = mon.NewMetricDaemonWriter(defaultMetrics...)
	s.application = application

	return nil
}

func (s *SubscriberCallback) GetModel(attributes map[string]interface{}) interface{} {
	var err error
	var spec *ModelSpecification
	var transformer ModelTransformer

	if spec, err = getModelSpecification(attributes); err != nil {
		return nil
	}

	if transformer, err = s.getTransformer(spec); err != nil {
		return nil
	}

	return transformer.GetInput()
}

func (s *SubscriberCallback) Consume(ctx context.Context, input interface{}, attributes map[string]interface{}) (bool, error) {
	var err error
	var model Model
	var spec *ModelSpecification
	var transformer ModelTransformer
	var output Output

	if spec, err = getModelSpecification(attributes); err != nil {
		return false, fmt.Errorf("can not read model specifications from the message attributes: %w", err)
	}

	if transformer, err = s.getTransformer(spec); err != nil {
		return false, err
	}

	if model, err = transformer.Transform(ctx, input); err != nil {
		return false, err
	}

	logger := s.logger.WithContext(ctx).WithFields(mon.Fields{
		"modelId": spec.ModelId,
		"type":    spec.CrudType,
		"version": spec.Version,
	})

	if model == nil {
		logger.Infof("skipping %s op for subscription for modelId %s and version %d", spec.CrudType, spec.ModelId, spec.Version)
		return true, nil
	}

	if output, err = s.getOutput(spec); err != nil {
		return false, err
	}

	err = output.Persist(ctx, model, spec.CrudType)
	s.writeMetric(err, spec)

	if err != nil {
		return false, fmt.Errorf("can not persist subscription of model %s and version %d: %w", spec.ModelId, spec.Version, err)
	}

	logger.Infof("persisted %s op for subscription for modelId %s and version %d with id %v", spec.CrudType, spec.ModelId, spec.Version, model.GetId())

	return true, nil
}

func (s *SubscriberCallback) getTransformer(spec *ModelSpecification) (ModelTransformer, error) {
	var ok bool

	if _, ok = s.transformers[spec.ModelId]; !ok {
		return nil, fmt.Errorf("there is no transformer for modelId %s", spec.ModelId)
	}

	if _, ok = s.transformers[spec.ModelId][spec.Version]; !ok {
		return nil, fmt.Errorf("there is no transformer for modelId %s and version %d", spec.ModelId, spec.Version)
	}

	return s.transformers[spec.ModelId][spec.Version], nil
}

func (s *SubscriberCallback) getOutput(spec *ModelSpecification) (Output, error) {
	var ok bool

	if _, ok = s.transformers[spec.ModelId]; !ok {
		return nil, fmt.Errorf("there is no output for modelId %s", spec.ModelId)
	}

	if _, ok = s.transformers[spec.ModelId][spec.Version]; !ok {
		return nil, fmt.Errorf("there is no output for modelId %s and version %d", spec.ModelId, spec.Version)
	}

	return s.outputs[spec.ModelId][spec.Version], nil
}

func (s *SubscriberCallback) writeMetric(err error, spec *ModelSpecification) {
	metricName := MetricNameSuccess

	if err != nil {
		metricName = MetricNameFailure
	}

	s.metric.WriteOne(&mon.MetricDatum{
		Priority:   mon.PriorityHigh,
		Timestamp:  time.Now(),
		MetricName: metricName,
		Dimensions: map[string]string{
			"Application": s.application,
			"ModelId":     spec.ModelId,
		},
		Unit:  mon.UnitCount,
		Value: 1.0,
	})
}

func (s *SubscriberCallback) getDefaultMetrics(application string) []*mon.MetricDatum {
	defaults := make([]*mon.MetricDatum, 0)

	for modelId := range s.transformers {
		success := &mon.MetricDatum{
			Priority:   mon.PriorityHigh,
			MetricName: MetricNameSuccess,
			Dimensions: map[string]string{
				"Application": application,
				"ModelId":     modelId,
			},
			Unit:  mon.UnitCount,
			Value: 0.0,
		}

		failure := &mon.MetricDatum{
			Priority:   mon.PriorityHigh,
			MetricName: MetricNameFailure,
			Dimensions: map[string]string{
				"Application": application,
				"ModelId":     modelId,
			},
			Unit:  mon.UnitCount,
			Value: 0.0,
		}

		defaults = append(defaults, success, failure)
	}

	return defaults
}
