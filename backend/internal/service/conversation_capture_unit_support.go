//go:build unit

package service

import (
	"context"
	"time"
)

type conversationCaptureUnitWriter struct {
	records chan<- *CaptureRecord
}

func (w *conversationCaptureUnitWriter) Write(_ context.Context, item *archiveWriteItem) error {
	if item == nil {
		return nil
	}
	if w != nil && w.records != nil {
		w.records <- item.record
	}
	item.completeSuccess()
	return nil
}

func (*conversationCaptureUnitWriter) Stop() {}

// NewConversationCapturePoolForUnitTest builds an in-memory capture sink for
// cross-package handler tests compiled with the unit tag.
func NewConversationCapturePoolForUnitTest(records chan<- *CaptureRecord) *ConversationCapturePool {
	return newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1,
		QueueSize:   16,
	}, &conversationCaptureUnitWriter{records: records})
}

// InstallOpenAIAccountSchedulerForUnitTest installs a scheduler spy and a
// short-lived enabled runtime setting so cross-package handler tests exercise
// the real selection/report call sites without a settings repository.
func (s *OpenAIGatewayService) InstallOpenAIAccountSchedulerForUnitTest(scheduler OpenAIAccountScheduler) func() {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:   true,
		expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	if s != nil {
		s.openaiScheduler = scheduler
		s.openaiSchedulerOnce.Do(func() {})
	}
	return resetOpenAIAdvancedSchedulerSettingCacheForTest
}
