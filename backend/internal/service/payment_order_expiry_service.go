package service

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const expiryCheckTimeout = 30 * time.Second

const (
	// paymentOrderExpiryLeaderLockKey gates the periodic payment sweeps so
	// that only one instance issues the upstream payment-provider calls per cycle.
	paymentOrderExpiryLeaderLockKey = "payment:order:expiry:leader"
	// paymentOrderExpiryLeaderLockTTL covers all three sweeps at expiryCheckTimeout.
	paymentOrderExpiryLeaderLockTTL = 3 * time.Minute
)

// PaymentOrderExpiryService periodically expires timed-out payment orders.
type PaymentOrderExpiryService struct {
	paymentSvc *PaymentService
	interval   time.Duration
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup

	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

func NewPaymentOrderExpiryService(paymentSvc *PaymentService, interval time.Duration) *PaymentOrderExpiryService {
	return &PaymentOrderExpiryService{
		paymentSvc: paymentSvc,
		interval:   interval,
		stopCh:     make(chan struct{}),
		instanceID: uuid.NewString(),
	}
}

// SetLeaderLock injects the leader-lock cache and DB used to elect a single
// instance for the periodic reconcile/expiry sweep. When both are nil the job
// runs ungated (single-instance / test behavior).
func (s *PaymentOrderExpiryService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

func (s *PaymentOrderExpiryService) Start() {
	if s == nil || s.paymentSvc == nil || s.interval <= 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		holdCursor := s.runOnce(easyPayHoldCursor{})
		for {
			select {
			case <-ticker.C:
				holdCursor = s.runOnce(holdCursor)
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *PaymentOrderExpiryService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.wg.Wait()
}

func (s *PaymentOrderExpiryService) runOnce(holdCursor easyPayHoldCursor) easyPayHoldCursor {
	// Multi-instance guard: only the leader reconciles/expires orders per cycle,
	// avoiding N× upstream payment-provider API calls and update races.
	lockCtx, lockCancel := context.WithTimeout(context.Background(), 2*time.Second)
	release, ok := tryAcquireSingletonLeaderLock(lockCtx, s.lockCache, s.db, paymentOrderExpiryLeaderLockKey, s.instanceID, paymentOrderExpiryLeaderLockTTL)
	lockCancel()
	if !ok {
		return holdCursor
	}
	defer release()

	reconcileCtx, cancel := context.WithTimeout(context.Background(), expiryCheckTimeout)
	recovered, err := s.paymentSvc.ReconcilePendingProviderOrders(reconcileCtx)
	cancel()
	if err != nil {
		slog.Warn("[PaymentOrderExpiry] failed to reconcile pending provider orders", "error", err)
	} else if recovered > 0 {
		slog.Info("[PaymentOrderExpiry] reconciled paid provider orders", "count", recovered)
	}

	expireCtx, cancel := context.WithTimeout(context.Background(), expiryCheckTimeout)
	expired, err := s.paymentSvc.ExpireTimedOutOrders(expireCtx)
	cancel()
	if err != nil {
		slog.Error("[PaymentOrderExpiry] failed to expire orders", "error", err)
	} else if expired > 0 {
		slog.Info("[PaymentOrderExpiry] expired timed-out orders", "count", expired)
	}

	holdCtx, cancel := context.WithTimeout(context.Background(), expiryCheckTimeout)
	defer cancel()
	next, err := s.paymentSvc.reconcileExpiredEasyPayHolds(holdCtx, holdCursor)
	if err != nil {
		slog.Warn("[PaymentOrderExpiry] failed to reconcile EasyPay confirmation holds", "error", err)
	}
	return next
}
