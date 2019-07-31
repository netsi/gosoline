// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import cfg "github.com/applike/gosoline/pkg/cfg"
import context "context"
import mock "github.com/stretchr/testify/mock"
import mon "github.com/applike/gosoline/pkg/mon"
import stream "github.com/applike/gosoline/pkg/stream"

// ConsumerCallback is an autogenerated mock type for the ConsumerCallback type
type ConsumerCallback struct {
	mock.Mock
}

// Boot provides a mock function with given fields: config, logger
func (_m *ConsumerCallback) Boot(config cfg.Config, logger mon.Logger) {
	_m.Called(config, logger)
}

// Consume provides a mock function with given fields: ctx, msg
func (_m *ConsumerCallback) Consume(ctx context.Context, msg *stream.Message) (bool, error) {
	ret := _m.Called(ctx, msg)

	var r0 bool
	if rf, ok := ret.Get(0).(func(context.Context, *stream.Message) bool); ok {
		r0 = rf(ctx, msg)
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, *stream.Message) error); ok {
		r1 = rf(ctx, msg)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
