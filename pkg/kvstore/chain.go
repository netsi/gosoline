package kvstore

import (
	"context"
	"fmt"
	"github.com/applike/gosoline/pkg/cfg"
	"github.com/applike/gosoline/pkg/mon"
	"github.com/applike/gosoline/pkg/refl"
)

type ChainKvStore struct {
	logger   mon.Logger
	factory  func(factory Factory, settings *Settings) KvStore
	chain    []KvStore
	settings *Settings

	missingCacheEnabled bool
	missingCache        *InMemoryKvStore
}

var noValue = &struct{}{}

func NewChainKvStore(config cfg.Config, logger mon.Logger, missingCacheEnabled bool, settings *Settings) *ChainKvStore {
	settings.PadFromConfig(config)
	factory := buildFactory(config, logger)

	var missingCache *InMemoryKvStore
	if missingCacheEnabled {
		missingCache = NewInMemoryKvStore(config, logger, settings).(*InMemoryKvStore)
	}

	return NewChainKvStoreWithInterfaces(logger, factory, missingCacheEnabled, missingCache, settings)
}

func NewChainKvStoreWithInterfaces(logger mon.Logger, factory func(Factory, *Settings) KvStore, missingCacheEnabled bool, missingCache *InMemoryKvStore, settings *Settings) *ChainKvStore {
	return &ChainKvStore{
		logger:              logger,
		factory:             factory,
		chain:               make([]KvStore, 0),
		settings:            settings,
		missingCache:        missingCache,
		missingCacheEnabled: missingCacheEnabled,
	}
}

func (s *ChainKvStore) Add(elementFactory Factory) {
	store := s.factory(elementFactory, s.settings)
	s.AddStore(store)
}

func (s *ChainKvStore) AddStore(store KvStore) {
	s.chain = append(s.chain, store)
}

func (s *ChainKvStore) Contains(ctx context.Context, key interface{}) (bool, error) {
	lastElementIndex := len(s.chain) - 1

	if s.missingCacheEnabled {
		// check if we can short circuit the whole deal
		exists, err := s.missingCache.Contains(ctx, key)

		if err != nil {
			s.logger.WithContext(ctx).Warnf("failed to read from missing value cache: %s", err.Error())
		}

		if exists {
			return false, nil
		}
	}

	for i, element := range s.chain {
		exists, err := element.Contains(ctx, key)

		if err != nil {
			// return error only if last element fails
			if i == lastElementIndex {
				return false, fmt.Errorf("could not check existence of %s from kvstore %T: %w", key, element, err)
			}

			s.logger.WithContext(ctx).Warnf("could not check existence of %s from kvstore %T: %s", key, element, err.Error())
		}

		if exists {
			return true, nil
		}
	}

	// Cache empty value if no result was found
	if s.missingCacheEnabled {
		if err := s.missingCache.Put(ctx, key, noValue); err != nil {
			s.logger.WithContext(ctx).Warnf("failed to write to missing value cache: %s", err.Error())
		}
	}

	return false, nil
}

func (s *ChainKvStore) Get(ctx context.Context, key interface{}, value interface{}) (bool, error) {
	if s.missingCacheEnabled {
		// check if we can short circuit the whole deal
		exists, err := s.missingCache.Contains(ctx, key)

		if err != nil {
			s.logger.WithContext(ctx).Warnf("failed to read from missing value cache: %s", err.Error())
		}

		if exists {
			return false, nil
		}
	}

	lastElementIndex := len(s.chain) - 1
	foundInIndex := lastElementIndex + 1
	var exists bool

	for i, element := range s.chain {
		var err error
		exists, err = element.Get(ctx, key, value)

		if err != nil {
			// return error only if last element fails
			if i == lastElementIndex {
				return false, fmt.Errorf("could not get %s from kvstore %T: %w", key, element, err)
			}

			s.logger.WithContext(ctx).Warnf("could not get %s from kvstore %T: %s", key, element, err.Error())
		}

		if exists {
			foundInIndex = i

			break
		}
	}

	// Cache empty value if no result was found
	if s.missingCacheEnabled && !exists {
		if err := s.missingCache.Put(ctx, key, noValue); err != nil {
			s.logger.WithContext(ctx).Warnf("failed to write to missing value cache: %s", err.Error())
		}
	}

	if !exists {
		return false, nil
	}

	// propagate to the lower cache levels
	for i := foundInIndex - 1; i >= 0; i-- {
		err := s.chain[i].Put(ctx, key, value)

		if err != nil {
			s.logger.WithContext(ctx).Warnf("could not put %s to kvstore %T: %s", key, s.chain[i], err.Error())
		}
	}

	return true, nil
}

func (s *ChainKvStore) GetBatch(ctx context.Context, keys interface{}, values interface{}) ([]interface{}, error) {
	todo, err := refl.InterfaceToInterfaceSlice(keys)
	var cachedMissing []interface{}

	if err != nil {
		return nil, fmt.Errorf("can not morph keys to slice of interfaces: %w", err)
	}

	if s.missingCacheEnabled {
		cachedMissingMap := make(map[string]interface{})
		todo, err = s.missingCache.GetBatch(ctx, todo, cachedMissingMap)

		if err != nil {
			s.logger.WithContext(ctx).Warnf("failed to read from missing value cache: %s", err.Error())
		}

		for k := range cachedMissingMap {
			cachedMissing = append(cachedMissing, k)
		}
	}

	if len(todo) == 0 {
		return cachedMissing, nil
	}

	lastElementIndex := len(s.chain) - 1
	refill := make(map[int][]interface{})
	foundInIndex := lastElementIndex + 1

	for i, element := range s.chain {
		var err error
		refill[i], err = element.GetBatch(ctx, todo, values)

		if err != nil {
			// return error only if last element fails
			if i == lastElementIndex {
				return nil, fmt.Errorf("could not get batch from kvstore %T: %w", element, err)
			}

			s.logger.WithContext(ctx).Warnf("could not get batch from kvstore %T: %s", element, err.Error())
			refill[i] = todo
		}

		todo = refill[i]

		if len(todo) == 0 {
			foundInIndex = i

			break
		}
	}

	mii, err := refl.InterfaceToMapInterfaceInterface(values)

	if err != nil {
		return nil, fmt.Errorf("can not cast result values from %T to map[interface{}]interface{}: %w", values, err)
	}

	// propagate to the lower cache levels
	for i := foundInIndex - 1; i >= 0; i-- {
		if len(refill[i]) == 0 {
			continue
		}

		missingInElement := make(map[interface{}]interface{})

		for _, key := range refill[i] {
			if val, ok := mii[key]; ok {
				missingInElement[key] = val
			}
		}

		if len(missingInElement) == 0 {
			continue
		}

		err = s.chain[i].PutBatch(ctx, missingInElement)

		if err != nil {
			s.logger.WithContext(ctx).Warnf("could not put batch to kvstore %T: %s", s.chain[i], err.Error())
		}
	}

	// store missing keys if enabled
	if s.missingCacheEnabled && len(todo) > 0 {
		missingValues := make(map[interface{}]interface{}, len(todo))

		for _, key := range todo {
			missingValues[key] = noValue
		}

		err = s.missingCache.PutBatch(ctx, missingValues)

		if err != nil {
			s.logger.WithContext(ctx).Warnf("could not put batch to empty value cache: %w", err.Error())
		}
	}

	missing := make([]interface{}, 0, len(todo)+len(cachedMissing))
	missing = append(missing, todo...)
	missing = append(missing, cachedMissing...)

	return missing, nil
}

func (s *ChainKvStore) Put(ctx context.Context, key interface{}, value interface{}) error {
	lastElementIndex := len(s.chain) - 1

	for i := 0; i <= lastElementIndex; i++ {
		err := s.chain[i].Put(ctx, key, value)

		if err != nil {
			// return error only if last element fails
			if i == lastElementIndex {
				return fmt.Errorf("could not put %s to kvstore %T: %w", key, s.chain[i], err)
			}

			s.logger.WithContext(ctx).Warnf("could not put %s to kvstore %T: %s", key, s.chain[i], err.Error())
		}
	}

	// remove the value from the missing value cache only after we persisted it
	// otherwise, we might remove it, some other thread adds it again and then we insert
	// it into the backing stores
	if s.missingCacheEnabled {
		if err := s.missingCache.Delete(ctx, key); err != nil {
			s.logger.WithContext(ctx).Warnf("could not erase cached empty value for key %s: %s", key, err.Error())
		}
	}

	return nil
}

func (s *ChainKvStore) PutBatch(ctx context.Context, values interface{}) error {
	lastElementIndex := len(s.chain) - 1

	for i := 0; i <= lastElementIndex; i++ {
		err := s.chain[i].PutBatch(ctx, values)

		if err != nil {
			// return error only if last element fails
			if i == lastElementIndex {
				return fmt.Errorf("could not put batch to kvstore %T: %w", s.chain[i], err)
			}

			s.logger.WithContext(ctx).Warnf("could not put batch to kvstore %T: %s", s.chain[i], err.Error())
		}
	}

	if s.missingCacheEnabled {
		mii, err := refl.InterfaceToMapInterfaceInterface(values)

		if err != nil {
			return fmt.Errorf("can not cast values from %T to map[interface{}]interface{}: %w", values, err)
		}

		for key := range mii {
			if err := s.missingCache.Delete(ctx, key); err != nil {
				s.logger.WithContext(ctx).Warnf("could not erase cached empty value for key %T %v: %s", key, key, err.Error())
			}
		}
	}

	return nil
}
