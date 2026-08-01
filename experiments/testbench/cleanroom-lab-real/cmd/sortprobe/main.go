// sortprobe 冻结 v1：NFKC、Unicode case-fold、数字自然序、Unicode 字节回退；读音不进入默认协议。
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type item struct {
	Label  string `json:"label"`
	WorkID string `json:"work_id"`
	IsNull bool   `json:"is_null"`
	Key    string `json:"key_hex"`
}
type report struct {
	SchemaVersion       int             `json:"schema_version"`
	SortProtocolVersion int             `json:"sort_protocol_version"`
	Nulls               string          `json:"nulls"`
	TieBreak            string          `json:"tie_break"`
	ReadingOrderDefault bool            `json:"reading_order_default"`
	Items               []item          `json:"items"`
	Checks              map[string]bool `json:"checks"`
}

func main() {
	out := flag.String("out", "results/sort-v1.json", "result JSON")
	flag.Parse()
	items := []item{{"file10", "w10", false, ""}, {"file2", "w02", false, ""}, {"Ｆｉｌｅ１", "w01", false, ""}, {"file1", "w00", false, ""}, {"阿部", "w-zh-2", false, ""}, {"安部", "w-zh-1", false, ""}, {"あべ", "w-ja", false, ""}, {"😀2", "w-e2", false, ""}, {"😀10", "w-e10", false, ""}, {"", "w-null", true, ""}}
	for i := range items {
		if !items[i].IsNull {
			items[i].Key = hex.EncodeToString(key(items[i].Label))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsNull != items[j].IsNull {
			return !items[i].IsNull
		}
		c := bytes.Compare(decode(items[i].Key), decode(items[j].Key))
		if c != 0 {
			return c < 0
		}
		return items[i].WorkID < items[j].WorkID
	})
	positions := map[string]int{}
	for i, x := range items {
		positions[x.Label+"|"+x.WorkID] = i
	}
	r := report{1, 1, "last", "ORDER BY sort_key, work_id", false, items, map[string]bool{
		"natural_2_before_10":         positions["file2|w02"] < positions["file10|w10"],
		"nfkc_fullwidth_equivalent":   bytes.Equal(key("Ｆｉｌｅ１"), key("File1")),
		"case_fold_equivalent":        bytes.Equal(key("FILE1"), key("file1")),
		"stable_id_breaks_equal_keys": positions["file1|w00"] < positions["Ｆｉｌｅ１|w01"],
		"emoji_natural_order":         positions["😀2|w-e2"] < positions["😀10|w-e10"],
		"nulls_last":                  items[len(items)-1].IsNull,
	}}
	b, e := json.MarshalIndent(r, "", "  ")
	must(e)
	must(os.MkdirAll(filepath.Dir(*out), 0o755))
	must(os.WriteFile(*out, b, 0o644))
	fmt.Printf("sort-v1 checks=%v items=%d\n", all(r.Checks), len(items))
}

func key(s string) []byte {
	s = cases.Fold().String(norm.NFKC.String(s))
	var out bytes.Buffer
	for i := 0; i < len(s); {
		r, size := rune(s[i]), 1
		if s[i] >= 0x80 {
			r, size = decodeRune(s[i:])
		}
		if unicode.IsDigit(r) && r >= '0' && r <= '9' {
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			digits := s[i:j]
			trim := bytes.TrimLeft([]byte(digits), "0")
			if len(trim) == 0 {
				trim = []byte{'0'}
			}
			out.WriteByte(0)
			_ = binary.Write(&out, binary.BigEndian, uint32(len(trim)))
			out.Write(trim)
			_ = binary.Write(&out, binary.BigEndian, uint32(len(digits)))
			i = j
			continue
		}
		out.WriteByte(1)
		out.WriteString(s[i : i+size])
		i += size
	}
	return out.Bytes()
}
func decodeRune(s string) (rune, int) {
	for n := 1; n <= 4 && n <= len(s); n++ {
		r := []rune(s[:n])
		if len(r) == 1 && r[0] != unicode.ReplacementChar {
			return r[0], n
		}
	}
	return unicode.ReplacementChar, 1
}
func decode(s string) []byte { b, _ := hex.DecodeString(s); return b }
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func all(m map[string]bool) bool {
	for _, v := range m {
		if !v {
			return false
		}
	}
	return true
}
