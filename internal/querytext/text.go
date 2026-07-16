package querytext

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var fold = cases.Fold()

type Document struct {
	NormalizedOriginal string
	CJKTokens          string
	LatinTokens        string
	SortTitleKey       string
}

type SearchPlan struct {
	NormalizedQuery string
	FTSQuery        string
	TooShort        bool
}

func BuildDocument(title, creator string, tags, filenames []string) Document {
	parts := []string{title, creator}
	parts = append(parts, tags...)
	parts = append(parts, filenames...)
	normalized := Normalize(strings.Join(parts, "\n"))
	return Document{
		NormalizedOriginal: normalized,
		CJKTokens:          strings.Join(CJKBigramTokens(normalized), " "),
		LatinTokens:        strings.Join(TrigramTokens(normalized), " "),
		SortTitleKey:       NaturalSortKey(title),
	}
}

func Normalize(value string) string { return fold.String(norm.NFKC.String(value)) }

func PlanSearch(query string) SearchPlan {
	normalized := Normalize(strings.TrimSpace(query))
	if normalized == "" {
		return SearchPlan{}
	}
	cjkCount := 0
	for _, value := range []rune(normalized) {
		if isCJK(value) {
			cjkCount++
		}
	}
	if cjkCount == 1 && len([]rune(normalized)) == 1 {
		return SearchPlan{NormalizedQuery: normalized, TooShort: true}
	}
	var clauses []string
	if tokens := CJKBigramTokens(normalized); len(tokens) > 0 {
		clauses = append(clauses, "cjk_bigram_token_text:("+quotedAND(tokens)+")")
	}
	if tokens := TrigramTokens(normalized); len(tokens) > 0 {
		clauses = append(clauses, "latin_trigram_token_text:("+quotedAND(tokens)+")")
	}
	return SearchPlan{NormalizedQuery: normalized, FTSQuery: strings.Join(clauses, " OR ")}
}

func CJKBigramTokens(value string) []string {
	runes := []rune(Normalize(value))
	var tokens []string
	for index := 0; index+1 < len(runes); index++ {
		if isCJK(runes[index]) && isCJK(runes[index+1]) {
			tokens = append(tokens, encodedToken(runes[index:index+2]))
		}
	}
	return unique(tokens)
}

func TrigramTokens(value string) []string {
	runes := []rune(Normalize(value))
	var tokens []string
	for index := 0; index+2 < len(runes); index++ {
		window := runes[index : index+3]
		if unicode.IsSpace(window[0]) || unicode.IsSpace(window[1]) || unicode.IsSpace(window[2]) {
			continue
		}
		tokens = append(tokens, encodedToken(window))
	}
	return unique(tokens)
}

func NaturalSortKey(value string) string {
	runes := []rune(Normalize(value))
	var output strings.Builder
	for index := 0; index < len(runes); {
		if runes[index] >= '0' && runes[index] <= '9' {
			end := index + 1
			for end < len(runes) && runes[end] >= '0' && runes[end] <= '9' {
				end++
			}
			raw := string(runes[index:end])
			significant := strings.TrimLeft(raw, "0")
			if significant == "" {
				significant = "0"
			}
			fmt.Fprintf(&output, "1%08x:%s:%08x;", len(significant), significant, len(raw))
			index = end
			continue
		}
		end := index + 1
		for end < len(runes) && !(runes[end] >= '0' && runes[end] <= '9') {
			end++
		}
		output.WriteString("0" + hex.EncodeToString([]byte(string(runes[index:end]))) + ";")
		index = end
	}
	return output.String()
}

func encodedToken(runes []rune) string {
	var output strings.Builder
	output.WriteByte('u')
	for _, value := range runes {
		fmt.Fprintf(&output, "%08x", value)
	}
	return output.String()
}

func quotedAND(tokens []string) string {
	quoted := make([]string, len(tokens))
	for index, token := range tokens {
		quoted[index] = `"` + token + `"`
	}
	return strings.Join(quoted, " AND ")
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func isCJK(value rune) bool {
	return value >= 0x3400 && value <= 0x9fff || value >= 0xf900 && value <= 0xfaff || value >= 0x3040 && value <= 0x30ff || value >= 0xac00 && value <= 0xd7af
}
