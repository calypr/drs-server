package postgres

import (
	"fmt"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
)

func postgresStringVal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
