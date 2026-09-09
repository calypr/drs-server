package postgres

import (
	"strings"
	"testing"
)

func TestBulkObjectConditionHasBalancedParentheses(t *testing.T) {
	condition, _ := (postgresDialect{}).BulkObjectCondition(
		[]string{"object"}, nil, nil, nil, 1,
	)
	if opens, closes := strings.Count(condition, "("), strings.Count(condition, ")"); opens != closes {
		t.Fatalf("unbalanced SQL condition: %d opening parentheses, %d closing parentheses\n%s", opens, closes, condition)
	}
}
