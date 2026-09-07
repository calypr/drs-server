package sqlite

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/models"
)

const sqliteMaxParams = 900

func defaultProvider(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "s3"
	}
	return provider
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func uniqueObjectsByID(objs []models.InternalObject) []models.InternalObject {
	seen := make(map[string]struct{}, len(objs))
	out := make([]models.InternalObject, 0, len(objs))
	for _, o := range objs {
		if _, ok := seen[o.Id]; ok {
			continue
		}
		seen[o.Id] = struct{}{}
		out = append(out, o)
	}
	return out
}

func execSQLiteDeleteByIDs(tx *sql.Tx, table string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for start := 0; start < len(ids); start += sqliteMaxParams {
		end := start + sqliteMaxParams
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		args := make([]interface{}, 0, len(chunk))
		for _, id := range chunk {
			args = append(args, id)
		}
		query := fmt.Sprintf("DELETE FROM %s WHERE object_id IN (%s)", table, makePlaceholders(len(chunk)))
		if _, err := tx.Exec(query, args...); err != nil {
			return err
		}
	}
	return nil
}

func safeSliceCapacity(parts ...int) (int, error) {
	total := int64(0)
	for _, part := range parts {
		if part < 0 {
			return 0, fmt.Errorf("negative capacity component: %d", part)
		}
		total += int64(part)
		if total > int64(math.MaxInt) {
			return 0, fmt.Errorf("capacity too large: %d", total)
		}
	}
	return int(total), nil
}

func execSQLiteBulkInsert(tx *sql.Tx, prefix string, rowPlaceholder string, rowArity int, args []interface{}, suffix string) error {
	if len(args) == 0 {
		return nil
	}
	rows := len(args) / rowArity
	maxRowsPerStmt := sqliteMaxParams / rowArity
	if maxRowsPerStmt < 1 {
		maxRowsPerStmt = 1
	}

	for rowStart := 0; rowStart < rows; rowStart += maxRowsPerStmt {
		rowEnd := rowStart + maxRowsPerStmt
		if rowEnd > rows {
			rowEnd = rows
		}
		stmtRows := rowEnd - rowStart
		stmtArgs := args[rowStart*rowArity : rowEnd*rowArity]
		values := make([]string, stmtRows)
		for i := 0; i < stmtRows; i++ {
			values[i] = rowPlaceholder
		}
		query := prefix + strings.Join(values, ",") + suffix
		if _, err := tx.Exec(query, stmtArgs...); err != nil {
			return err
		}
	}
	return nil
}

func latestTime(ts ...*time.Time) *time.Time {
	var latest *time.Time
	for _, t := range ts {
		if t == nil {
			continue
		}
		if latest == nil || t.After(*latest) {
			copyT := *t
			latest = &copyT
		}
	}
	return latest
}
