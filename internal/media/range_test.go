package media_test

import (
	"errors"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
)

func TestSingleRangeSemantics(t *testing.T) {
	cases := map[string]media.ByteRange{
		"bytes=0-3":  {Start: 0, End: 3},
		"bytes=4-":   {Start: 4, End: 9},
		"bytes=-3":   {Start: 7, End: 9},
		"bytes=8-99": {Start: 8, End: 9},
	}
	for header, expected := range cases {
		actual, present, err := media.ParseSingleRange(header, 10)
		if err != nil || !present || actual != expected {
			t.Fatalf("%s => %+v %t %v", header, actual, present, err)
		}
	}
	for _, header := range []string{"bytes=", "bytes=10-", "bytes=4-2", "bytes=0-1,3-4", "items=0-1"} {
		_, _, err := media.ParseSingleRange(header, 10)
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeRangeInvalid {
			t.Fatalf("%s 未返回 RANGE_INVALID: %v", header, err)
		}
	}
}
