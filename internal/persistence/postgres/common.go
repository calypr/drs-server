package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
)

// postgresContentRow and postgresMergeContentRowTx keep the metadata-focused
// regression test independent of the shared Store implementation. Runtime
// writes live in internal/persistence/store.
type postgresContentRow struct {
	id      string
	name    string
	updated time.Time
}

func postgresMergeContentRowTx(ctx context.Context, tx *sql.Tx, row postgresContentRow, obj *objects.Record, _ []string, _ []string) error {
	name := objects.CleanToBasename(postgresStringVal(obj.Name))
	if name == "" {
		name = row.name
	}
	if row.name != "" && name != "" && row.name != name {
		if _, err := tx.ExecContext(ctx, `
		INSERT INTO drs_object_name_alias (object_id, name_alias) VALUES ($1, $2)
		ON CONFLICT (object_id, name_alias) DO NOTHING`, row.id, row.name); err != nil {
			return err
		}
	}
	version, description := postgresStringVal(obj.Version), postgresStringVal(obj.Description)
	size := obj.Size
	updated := row.updated
	if obj.UpdatedTime != nil && obj.UpdatedTime.After(updated) {
		updated = *obj.UpdatedTime
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE drs_object SET size = $1, updated_time = $2, name = $3,
		version = $4, description = $5 WHERE id = $6`, size, updated, name, version, description, row.id)
	return err
}

func defaultProvider(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "s3"
	}
	return provider
}

func postgresPtr[T any](value T) *T {
	return &value
}

func postgresStringVal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func postgresTimeVal(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func uniqueObjectsByID(objs []objects.Record) []objects.Record {
	seen := make(map[string]struct{}, len(objs))
	out := make([]objects.Record, 0, len(objs))
	for _, o := range objs {
		if _, ok := seen[string(o.Id)]; ok {
			continue
		}
		seen[string(o.Id)] = struct{}{}
		out = append(out, o)
	}
	return out
}

func latestUsageTime(ts ...*time.Time) *time.Time {
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

// postgresScopeResourceCondition remains a dialect test helper. Shared Store
// reads use the same question-mark form and let Dialect.Rebind apply $n.
func postgresScopeResourceCondition(column, organization, project string) (string, []any, error) {
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(project) != "" {
		return column + " = ?", []any{resource}, nil
	}
	return "(" + column + " = ? OR " + column + " LIKE ? ESCAPE '\\')", []any{resource, postgresLikeEscape(resource+"/project/") + "%"}, nil
}

func postgresLikeEscape(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func postgresRebindQuestionPlaceholders(query string, start int) string {
	var b strings.Builder
	next := start
	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '?' {
			b.WriteString(fmt.Sprintf("$%d", next))
			next++
			continue
		}
		if r == '$' && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9' {
			j := i + 1
			number := 0
			for j < len(runes) && runes[j] >= '0' && runes[j] <= '9' {
				number = number*10 + int(runes[j]-'0')
				j++
			}
			if number >= next {
				next = number + 1
			}
			b.WriteString(string(runes[i:j]))
			i = j - 1
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func normalizeObjectNameAliases(obj *objects.Record) []string {
	if obj == nil {
		return nil
	}
	return objects.NormalizeNameAliases(postgresStringVal(obj.Name), obj.NameAliases)
}
