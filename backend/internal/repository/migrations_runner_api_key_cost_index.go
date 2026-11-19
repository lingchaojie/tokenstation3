package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

type usageLogIndexPartition struct {
	oid, parentOID int64
	schema, table  string
	partitioned    bool
}

// PostgreSQL can build leaf indexes concurrently and attach them to metadata
// indexes made with ON ONLY. An interrupted migration can leave either an
// invalid concurrent leaf index or an incomplete partitioned index.
func applyPartitionedUsageLogsAPIKeyCostIndex(ctx context.Context, db migrationConnection) (bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT p.relid::oid, COALESCE(p.parentrelid::oid, 0), ns.nspname, c.relname, c.relkind = 'p'
		FROM pg_partition_tree(to_regclass('usage_logs')) p
		JOIN pg_class c ON c.oid = p.relid
		JOIN pg_namespace ns ON ns.oid = c.relnamespace
		ORDER BY p.level, p.relid::oid
	`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	var partitions []usageLogIndexPartition
	for rows.Next() {
		var part usageLogIndexPartition
		if err := rows.Scan(&part.oid, &part.parentOID, &part.schema, &part.table, &part.partitioned); err != nil {
			return false, err
		}
		partitions = append(partitions, part)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(partitions) == 0 || !partitions[0].partitioned {
		return false, nil
	}

	indexes := make(map[int64]string, len(partitions))
	var attach []usageLogIndexPartition
	for _, part := range partitions {
		name := usageLogsAPIKeyCostIndex
		if part.parentOID != 0 {
			parentIndex := indexes[part.parentOID]
			var attachedIndex string
			err := db.QueryRowContext(ctx, `
				SELECT child.relname
				FROM pg_inherits inh
				JOIN pg_index idx ON idx.indexrelid = inh.inhrelid
				JOIN pg_class child ON child.oid = inh.inhrelid
				WHERE inh.inhparent = to_regclass($1) AND idx.indrelid = $2::oid
			`, parentIndex, part.oid).Scan(&attachedIndex)
			if err == nil {
				indexes[part.oid] = qualifiedIndexIdentifier(part.schema, attachedIndex)
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return true, fmt.Errorf("check attached index for %s: %w", part.table, err)
			}
			name = fmt.Sprintf("%s_p%d", name, part.oid)
			attach = append(attach, part)
		}
		if err := ensureUsageLogCostPartitionIndex(ctx, db, part, name); err != nil {
			return true, err
		}
		indexes[part.oid] = qualifiedIndexIdentifier(part.schema, name)
	}
	for i := len(attach) - 1; i >= 0; i-- {
		part := attach[i]
		if _, err := db.ExecContext(ctx, "ALTER INDEX "+indexes[part.parentOID]+" ATTACH PARTITION "+indexes[part.oid]); err != nil {
			return true, fmt.Errorf("attach usage cost index for %s: %w", part.table, err)
		}
	}
	var valid bool
	err = db.QueryRowContext(ctx, "SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass($1)", indexes[partitions[0].oid]).Scan(&valid)
	if err != nil {
		return true, fmt.Errorf("verify partitioned usage cost index: %w", err)
	}
	if !valid {
		return true, errors.New("partitioned usage cost index still has unindexed partitions")
	}
	return true, nil
}

func ensureUsageLogCostPartitionIndex(ctx context.Context, db migrationConnection, part usageLogIndexPartition, name string) error {
	index := qualifiedIndexIdentifier(part.schema, name)
	table := qualifiedIndexIdentifier(part.schema, part.table)
	create := "CREATE INDEX IF NOT EXISTS " + pq.QuoteIdentifier(name) + " ON ONLY "
	if !part.partitioned {
		var invalid bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_index
				WHERE indexrelid = to_regclass($1) AND indrelid = $2::oid AND NOT indisvalid
			)
		`, index, part.oid).Scan(&invalid)
		if err != nil {
			return fmt.Errorf("check invalid index %s: %w", index, err)
		}
		if invalid {
			if _, err := db.ExecContext(ctx, "DROP INDEX CONCURRENTLY IF EXISTS "+index); err != nil {
				return fmt.Errorf("drop invalid index %s: %w", index, err)
			}
		}
		create = "CREATE INDEX CONCURRENTLY IF NOT EXISTS " + pq.QuoteIdentifier(name) + " ON "
	}
	if _, err := db.ExecContext(ctx, create+table+" (api_key_id, created_at) INCLUDE (actual_cost)"); err != nil {
		return fmt.Errorf("create usage cost index %s: %w", index, err)
	}
	return nil
}

// The ordinary-table retry must inspect the same relation resolved by the
// migration SQL, including when it lives outside the public schema.
func dropInvalidUsageLogsAPIKeyCostIndex(ctx context.Context, db migrationConnection) error {
	var schema string
	err := db.QueryRowContext(ctx, `
		SELECT ns.nspname
		FROM pg_index i
		JOIN pg_class idx ON idx.oid = i.indexrelid
		JOIN pg_namespace ns ON ns.oid = idx.relnamespace
		WHERE i.indrelid = to_regclass('usage_logs')
		  AND idx.relname = $1 AND NOT i.indisvalid
	`, usageLogsAPIKeyCostIndex).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check invalid usage cost index: %w", err)
	}
	if _, err := db.ExecContext(ctx, "DROP INDEX CONCURRENTLY IF EXISTS "+qualifiedIndexIdentifier(schema, usageLogsAPIKeyCostIndex)); err != nil {
		return fmt.Errorf("drop invalid usage cost index: %w", err)
	}
	return nil
}

func qualifiedIndexIdentifier(schema, name string) string {
	return pq.QuoteIdentifier(schema) + "." + pq.QuoteIdentifier(name)
}
