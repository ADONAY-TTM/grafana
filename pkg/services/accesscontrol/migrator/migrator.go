package migrator

import (
	"context"
	"fmt"
	"strconv"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/grafana/grafana/pkg/infra/db"
	"github.com/grafana/grafana/pkg/infra/log"
	ac "github.com/grafana/grafana/pkg/services/accesscontrol"
	"github.com/grafana/grafana/pkg/services/authz/zanzana"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/services/sqlstore/session"
)

var (
	batchSize = 1000
)

const (
	maxLen = 40
)

func MigrateScopeSplit(db db.DB, log log.Logger) error {
	t := time.Now()
	ctx := context.Background()
	cnt := 0

	// Search for the permissions to update
	var permissions []ac.Permission
	if errFind := db.WithTransactionalDbSession(ctx, func(sess *sqlstore.DBSession) error {
		return sess.SQL("SELECT * FROM permission WHERE NOT scope = '' AND identifier = ''").Find(&permissions)
	}); errFind != nil {
		log.Error("Could not search for permissions to update", "migration", "scopeSplit", "error", errFind)
		return errFind
	}

	if len(permissions) == 0 {
		log.Debug("No permission require a scope split", "migration", "scopeSplit")
		return nil
	}

	errBatchUpdate := batch(len(permissions), batchSize, func(start, end int) error {
		n := end - start

		// IDs to remove
		delQuery := "DELETE FROM permission WHERE id IN ("
		delArgs := make([]any, 0, n)

		// Query to insert the updated permissions
		insertQuery := "INSERT INTO permission (id, role_id, action, scope, kind, attribute, identifier, created, updated) VALUES "
		insertArgs := make([]any, 0, 9*n)

		// Prepare batch of updated permissions
		for i := start; i < end; i++ {
			kind, attribute, identifier := permissions[i].SplitScope()

			// Trim to max length to avoid bootloop.
			// too long scopes will be truncated and the permission will become invalid.
			kind = trimToMaxLen(kind, maxLen)
			attribute = trimToMaxLen(attribute, maxLen)
			identifier = trimToMaxLen(identifier, maxLen)

			delQuery += "?,"
			delArgs = append(delArgs, permissions[i].ID)

			insertQuery += "(?, ?, ?, ?, ?, ?, ?, ?, ?),"
			insertArgs = append(insertArgs, permissions[i].ID, permissions[i].RoleID,
				permissions[i].Action, permissions[i].Scope,
				kind, attribute, identifier,
				permissions[i].Created, t,
			)
		}
		// Remove trailing ','
		insertQuery = insertQuery[:len(insertQuery)-1]

		// Remove trailing ',' and close brackets
		delQuery = delQuery[:len(delQuery)-1] + ")"

		// Batch update the permissions
		if errBatchUpdate := db.GetSqlxSession().WithTransaction(ctx, func(tx *session.SessionTx) error {
			if _, errDel := tx.Exec(ctx, delQuery, delArgs...); errDel != nil {
				log.Error("Error deleting permissions", "migration", "scopeSplit", "error", errDel)
				return errDel
			}
			if _, errInsert := tx.Exec(ctx, insertQuery, insertArgs...); errInsert != nil {
				log.Error("Error saving permissions", "migration", "scopeSplit", "error", errInsert)
				return errInsert
			}
			return nil
		}); errBatchUpdate != nil {
			log.Error("Error updating permission batch", "migration", "scopeSplit", "start", start, "end", end)
			return errBatchUpdate
		}

		cnt += end - start
		return nil
	})
	if errBatchUpdate != nil {
		log.Error("Could not migrate permissions", "migration", "scopeSplit", "total", len(permissions), "succeeded", cnt, "left", len(permissions)-cnt, "error", errBatchUpdate)
		return errBatchUpdate
	}

	log.Debug("Migrated permissions", "migration", "scopeSplit", "total", len(permissions), "succeeded", cnt, "in", time.Since(t))
	return nil
}

const permissionQuery = `
SELECT ur.user_id, p.action, p.kind, p.identifier, r.org_id FROM permission p
INNER JOIN role r on p.role_id = r.id
INNER JOIN user_role ur on r.id  = ur.role_id
WHERE p.action IN('dashboards:read')
AND r.name like 'managed:users:%'
LIMIT 1000;
`

func MigratePermissions(client zanzana.Client, store db.DB, log log.Logger) error {
	ctx := context.Background()
	type Permission struct {
		RoleName   string `xorm:"role_name"`
		OrgID      int64  `xorm:"org_id"`
		Action     string `xorm:"action"`
		Kind       string
		Identifier string
		UserID     int64 `xorm:"user_id"`
	}

	var permissions []Permission
	err := store.WithDbSession(ctx, func(sess *db.Session) error {
		return sess.SQL(permissionQuery).Find(&permissions)
	})

	if err != nil {
		return err
	}

	tuples := make([]*openfgav1.TupleKey, 0, len(permissions))
	for _, p := range permissions {
		user := zanzana.NewObject(zanzana.TypeUser, strconv.FormatInt(p.UserID, 10))
		tuple, ok := zanzana.TranslateToTuple(user, p.Action, p.Kind, p.Identifier, p.OrgID)
		if !ok {
			// FIXME(kalleep): Warning log for unsuported actions
			continue
		}

		tuples = append(tuples, tuple)
	}

	// TODO: how do we track already migrated permission
	err = client.Write(ctx, &openfgav1.WriteRequest{
		Writes: &openfgav1.WriteRequestWrites{
			TupleKeys: tuples,
		},
	})

	fmt.Println(err)

	return nil
}

func batch(count, batchSize int, eachFn func(start, end int) error) error {
	for i := 0; i < count; {
		end := i + batchSize
		if end > count {
			end = count
		}

		if err := eachFn(i, end); err != nil {
			return err
		}

		i = end
	}

	return nil
}

func trimToMaxLen(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
