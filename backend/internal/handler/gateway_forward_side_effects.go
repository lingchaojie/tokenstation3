package handler

import (
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// gatewayForwardSideEffectSubmitter keeps usage and capture submission coupled
// and exact-once for one upstream attempt. A pre-output failover has no result
// and therefore submits nothing; committed partial and successful results share
// the same guarded sink.
type gatewayForwardSideEffectSubmitter struct {
	once   sync.Once
	submit func(*service.ForwardResult)
}

type openAIForwardSideEffectSubmitter struct {
	once   sync.Once
	submit func(*service.OpenAIForwardResult)
}

func newOpenAIForwardSideEffectSubmitter(submit func(*service.OpenAIForwardResult)) *openAIForwardSideEffectSubmitter {
	return &openAIForwardSideEffectSubmitter{submit: submit}
}

func (s *openAIForwardSideEffectSubmitter) Submit(result *service.OpenAIForwardResult) {
	if s == nil || s.submit == nil || result == nil {
		return
	}
	s.once.Do(func() { s.submit(result) })
}

func newGatewayForwardSideEffectSubmitter(submit func(*service.ForwardResult)) *gatewayForwardSideEffectSubmitter {
	return &gatewayForwardSideEffectSubmitter{submit: submit}
}

func (s *gatewayForwardSideEffectSubmitter) Submit(result *service.ForwardResult) {
	if s == nil || s.submit == nil || result == nil {
		return
	}
	s.once.Do(func() { s.submit(result) })
}
