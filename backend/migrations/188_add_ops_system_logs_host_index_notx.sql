-- Migration 188 is intentionally non-transactional.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ops_system_logs_host_created_at
  ON ops_system_logs (host, created_at DESC);
