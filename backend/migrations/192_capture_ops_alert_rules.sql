-- Seed capture pipeline alert rules into the existing Ops evaluator.
-- Names are conflict keys so administrator-customized existing rules are preserved.

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '转存基础设施未就绪',
    '静态转存已配置，但当前评估实例的 ClickHouse writer 未就绪；值为 0 持续 2 分钟时触发。',
    true, 'capture_ready', '<', 1.0, 1, 2, 'P0', true, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '转存发生数据丢失',
    '最近 5 分钟任意转存丢失记录数大于 0 时触发；丢失数据不会自动补写。',
    true, 'capture_dropped_records', '>', 0.0, 5, 1, 'P1', true, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    'ClickHouse 转存写入失败',
    '最近 5 分钟发生 writer 不可用或 ClickHouse prepare、append、send 失败时触发；丢失数据不会自动补写。',
    true, 'capture_writer_failures', '>', 0.0, 5, 1, 'P0', true, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;
