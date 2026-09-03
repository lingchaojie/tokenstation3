package handler

import (
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// gatewayForwardSideEffectSubmitter keeps usage and capture submission coupled.
// Its sync.Once guard invokes the callback at most once during this in-process
// finalizer's lifetime, including when the callback fails or panics; it is not a
// distributed exactly-once guarantee. A pre-output failover has no result and
// therefore submits nothing. Usage-log durability remains best-effort and is
// separate from transactional billing idempotency.
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

// newOpenAIForwardSideEffectSubmitterWithEffects is the stable coupling point
// for capture and usage callbacks. Capture runs first; usage runs only when the
// caller's eligibility predicate permits it. The outer submitter supplies the
// per-finalizer at-most-once guard for the whole ordered pair.
func newOpenAIForwardSideEffectSubmitterWithEffects(
	capture func(*service.OpenAIForwardResult),
	usage func(*service.OpenAIForwardResult),
	shouldRecordUsage func(*service.OpenAIForwardResult) bool,
) *openAIForwardSideEffectSubmitter {
	return newOpenAIForwardSideEffectSubmitter(func(result *service.OpenAIForwardResult) {
		if capture != nil {
			capture(result)
		}
		if usage != nil && (shouldRecordUsage == nil || shouldRecordUsage(result)) {
			usage(result)
		}
	})
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

// newGatewayForwardSideEffectSubmitterWithEffects is the stable coupling point
// for capture and usage callbacks. See the OpenAI variant for ordering and
// eligibility semantics.
func newGatewayForwardSideEffectSubmitterWithEffects(
	capture func(*service.ForwardResult),
	usage func(*service.ForwardResult),
	shouldRecordUsage func(*service.ForwardResult) bool,
) *gatewayForwardSideEffectSubmitter {
	return newGatewayForwardSideEffectSubmitter(func(result *service.ForwardResult) {
		if capture != nil {
			capture(result)
		}
		if usage != nil && (shouldRecordUsage == nil || shouldRecordUsage(result)) {
			usage(result)
		}
	})
}

func (s *gatewayForwardSideEffectSubmitter) Submit(result *service.ForwardResult) {
	if s == nil || s.submit == nil || result == nil {
		return
	}
	s.once.Do(func() { s.submit(result) })
}
