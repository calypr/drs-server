package postgres

import (
	"fmt"
	"strings"
)

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
