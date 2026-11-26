package main

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tarektouati/made/internal/task"
)

// taskSuggestions returns at most three quoted names, ordered by closeness and
// then alphabetically. Suggestions are only hints: task lookup stays exact.
func taskSuggestions(tasks []task.Task, name string) string {
	const maxRunes = 128
	n := utf8.RuneCountInString(name)
	if n == 0 || n > maxRunes {
		return ""
	}
	query := []rune(strings.ToLower(name))
	limit := 2
	if n < 4 {
		limit = 1
	}
	type match struct {
		name     string
		distance int
	}
	var matches []match
	seen := make(map[string]bool)
	for _, t := range tasks {
		if t.Name == "" || seen[t.Name] || utf8.RuneCountInString(t.Name) > maxRunes {
			continue
		}
		seen[t.Name] = true
		candidate := strings.ToLower(t.Name)
		var distance int
		if n >= 2 && strings.HasPrefix(candidate, string(query)) {
			distance = 0
		} else {
			distance = taskEditDistance(query, []rune(candidate), limit)
		}
		if distance <= limit {
			matches = append(matches, match{t.Name, distance})
		}
	}
	slices.SortFunc(matches, func(a, b match) int {
		if c := cmp.Compare(a.distance, b.distance); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	quoted := make([]string, 0, min(len(matches), 3))
	for _, m := range matches[:min(len(matches), 3)] {
		quoted = append(quoted, strconv.Quote(m.name))
	}
	return strings.Join(quoted, ", ")
}

func taskEditDistance(a, b []rune, limit int) int {
	if len(a)-len(b) > limit || len(b)-len(a) > limit {
		return limit + 1
	}
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i, ar := range a {
		diagonal := row[0]
		row[0] = i + 1
		lowest := row[0]
		for j, br := range b {
			cost := 0
			if ar != br {
				cost = 1
			}
			above := row[j+1]
			row[j+1] = min(above+1, row[j]+1, diagonal+cost)
			diagonal = above
			lowest = min(lowest, row[j+1])
		}
		if lowest > limit {
			return limit + 1
		}
	}
	return row[len(b)]
}
