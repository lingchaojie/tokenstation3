package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type scopedAdminKeyRepository struct{ db *sql.DB }

func NewScopedAdminKeyRepository(db *sql.DB) service.ScopedAdminKeyRepository {
	return &scopedAdminKeyRepository{db: db}
}

const scopedAdminKeyColumns = `id,name,description,key_prefix,key_hash,enabled,expires_at,allowed_ips,policy,created_by,created_at,updated_at,last_used_at,last_used_ip,revoked_at`

func scanScopedAdminKey(row interface{ Scan(...any) error }) (*service.ScopedAdminKey, error) {
	k := &service.ScopedAdminKey{}
	var ips, policy []byte
	err := row.Scan(&k.ID, &k.Name, &k.Description, &k.KeyPrefix, &k.Hash, &k.Enabled, &k.ExpiresAt, &ips, &policy, &k.CreatedBy, &k.CreatedAt, &k.UpdatedAt, &k.LastUsedAt, &k.LastUsedIP, &k.RevokedAt)
	if err == sql.ErrNoRows {
		return nil, service.ErrScopedAdminKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(ips, &k.AllowedIPs); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(policy, &k.Policy); err != nil {
		return nil, err
	}
	return k, nil
}
func (r *scopedAdminKeyRepository) ListScopedAdminKeys(ctx context.Context) ([]*service.ScopedAdminKey, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT `+scopedAdminKeyColumns+` FROM scoped_admin_api_keys WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	items := []*service.ScopedAdminKey{}
	for rows.Next() {
		k, e := scanScopedAdminKey(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, k)
	}
	return items, rows.Err()
}
func (r *scopedAdminKeyRepository) GetScopedAdminKey(ctx context.Context, id string) (*service.ScopedAdminKey, error) {
	return scanScopedAdminKey(r.db.QueryRowContext(ctx, `SELECT `+scopedAdminKeyColumns+` FROM scoped_admin_api_keys WHERE id=$1`, id))
}
func (r *scopedAdminKeyRepository) InsertScopedAdminKey(ctx context.Context, k *service.ScopedAdminKey) error {
	ips, e := json.Marshal(k.AllowedIPs)
	if e != nil {
		return e
	}
	policy, e := json.Marshal(k.Policy)
	if e != nil {
		return e
	}
	// Share-lock the issuer before creating a key. Role and status changes lock
	// the same user row before revoking keys, so they cannot miss a concurrent issue.
	stored, err := scanScopedAdminKey(r.db.QueryRowContext(ctx, `
INSERT INTO scoped_admin_api_keys (id,name,description,key_prefix,key_hash,enabled,expires_at,allowed_ips,policy,created_by,created_at,updated_at)
SELECT $1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,u.id,$11,$12
FROM users u
WHERE u.id=$10 AND u.role=$13 AND u.status=$14 AND u.deleted_at IS NULL
FOR SHARE OF u
RETURNING `+scopedAdminKeyColumns, k.ID, k.Name, k.Description, k.KeyPrefix, k.Hash, k.Enabled, k.ExpiresAt, string(ips), string(policy), k.CreatedBy, k.CreatedAt, k.UpdatedAt, service.RoleAdmin, service.StatusActive))
	if errors.Is(err, service.ErrScopedAdminKeyNotFound) {
		return service.ErrScopedAdminKeyCreatorInactive
	}
	if err != nil {
		return err
	}
	*k = *stored
	return nil
}
func scopedAdminKeyResult(result sql.Result, e error) error {
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return service.ErrScopedAdminKeyNotFound
	}
	return nil
}
func (r *scopedAdminKeyRepository) UpdateScopedAdminKey(ctx context.Context, k *service.ScopedAdminKey) error {
	ips, e := json.Marshal(k.AllowedIPs)
	if e != nil {
		return e
	}
	policy, e := json.Marshal(k.Policy)
	if e != nil {
		return e
	}
	// Keep the client's revision in the write predicate and return the exact
	// persisted next revision, even when writes share a microsecond.
	stored, err := scanScopedAdminKey(r.db.QueryRowContext(ctx, `
UPDATE scoped_admin_api_keys SET name=$2,description=$3,enabled=$4,expires_at=$5,allowed_ips=$6,policy=$7,
    updated_at=GREATEST($8::timestamptz, updated_at + interval '1 microsecond')
WHERE id=$1 AND revoked_at IS NULL AND updated_at=$9
RETURNING `+scopedAdminKeyColumns, k.ID, k.Name, k.Description, k.Enabled, k.ExpiresAt, string(ips), string(policy), k.UpdatedAt, k.ExpectedUpdatedAt))
	if errors.Is(err, service.ErrScopedAdminKeyNotFound) {
		return service.ErrScopedAdminKeyConflict
	}
	if err != nil {
		return err
	}
	*k = *stored
	return nil
}
func (r *scopedAdminKeyRepository) TouchScopedAdminKey(ctx context.Context, id, expectedHash string, expectedUpdatedAt time.Time, ip string, now time.Time) error {
	// Preserve issuer-first locking during authorization as well as issuance.
	// The key CAS still rejects simultaneous rotation, revocation and policy edits.
	return scopedAdminKeyResult(r.db.ExecContext(ctx, `
WITH creator AS MATERIALIZED (
    SELECT u.id FROM users u JOIN scoped_admin_api_keys existing ON existing.created_by = u.id
    WHERE existing.id=$1 AND u.role=$6 AND u.status=$7 AND u.deleted_at IS NULL
    FOR SHARE OF u
)
UPDATE scoped_admin_api_keys SET last_used_at=$5,last_used_ip=$4
WHERE id=$1 AND key_hash=$2 AND updated_at=$3 AND revoked_at IS NULL AND enabled=true
  AND (expires_at IS NULL OR expires_at>$5) AND created_by=(SELECT id FROM creator)`,
		id, expectedHash, expectedUpdatedAt, ip, now, service.RoleAdmin, service.StatusActive))
}
func (r *scopedAdminKeyRepository) RevokeScopedAdminKey(ctx context.Context, id string, now time.Time) error {
	return scopedAdminKeyResult(r.db.ExecContext(ctx, `UPDATE scoped_admin_api_keys SET revoked_at=$2,enabled=false,updated_at=$2 WHERE id=$1 AND revoked_at IS NULL`, id, now))
}

// 删除仅隐藏列表项；原子检查撤销状态，不删除密钥记录或历史审计。
func (r *scopedAdminKeyRepository) DeleteScopedAdminKeyRecord(ctx context.Context, id string, now time.Time) error {
	return scopedAdminKeyResult(r.db.ExecContext(ctx, `UPDATE scoped_admin_api_keys SET deleted_at=$2,updated_at=$2 WHERE id=$1 AND revoked_at IS NOT NULL AND deleted_at IS NULL`, id, now))
}

// 轮换按读取时的摘要进行 CAS，同一旧版本只能成功轮换一次。
func (r *scopedAdminKeyRepository) RotateScopedAdminKeyHash(ctx context.Context, id, expectedHash, hash string, at time.Time) (*service.ScopedAdminKey, error) {
	// Pair the new revision with the same row's policy and IP restrictions.
	stored, err := scanScopedAdminKey(r.db.QueryRowContext(ctx, `
UPDATE scoped_admin_api_keys SET key_hash=$2, updated_at=GREATEST($3::timestamptz, updated_at + interval '1 microsecond')
WHERE id=$1 AND key_hash=$4 AND revoked_at IS NULL
RETURNING `+scopedAdminKeyColumns, id, hash, at, expectedHash))
	if errors.Is(err, service.ErrScopedAdminKeyNotFound) {
		return nil, service.ErrScopedAdminKeyConflict
	}
	return stored, err
}

// revokeScopedKeysByCreator runs in the transaction holding the affected user row locks.
func revokeScopedKeysByCreator(ctx context.Context, exec sqlExecutor, users []int64, at time.Time) error {
	if len(users) == 0 {
		return nil
	}
	if exec == nil {
		return errors.New("SQL executor is not configured")
	}
	_, err := exec.ExecContext(ctx, `
UPDATE scoped_admin_api_keys SET revoked_at=$2,enabled=false,updated_at=$2
WHERE created_by=ANY($1) AND revoked_at IS NULL`, pq.Array(users), at)
	return err
}
