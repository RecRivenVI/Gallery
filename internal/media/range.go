package media

import (
	"strconv"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

type ByteRange struct{ Start, End int64 }

func (r ByteRange) Length() int64 { return r.End - r.Start + 1 }

func ParseSingleRange(header string, size int64) (ByteRange, bool, error) {
	if header == "" {
		return ByteRange{}, false, nil
	}
	if size <= 0 || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return ByteRange{}, false, fault.New(fault.CodeRangeInvalid, false, nil)
	}
	value := strings.TrimPrefix(header, "bytes=")
	startText, endText, ok := strings.Cut(value, "-")
	if !ok || (startText == "" && endText == "") {
		return ByteRange{}, false, fault.New(fault.CodeRangeInvalid, false, nil)
	}
	var start, end int64
	var err error
	if startText == "" {
		suffix, parseErr := strconv.ParseInt(endText, 10, 64)
		if parseErr != nil || suffix <= 0 {
			return ByteRange{}, false, fault.New(fault.CodeRangeInvalid, false, nil)
		}
		if suffix > size {
			suffix = size
		}
		start, end = size-suffix, size-1
	} else {
		start, err = strconv.ParseInt(startText, 10, 64)
		if err != nil || start < 0 || start >= size {
			return ByteRange{}, false, fault.New(fault.CodeRangeInvalid, false, nil)
		}
		if endText == "" {
			end = size - 1
		} else {
			end, err = strconv.ParseInt(endText, 10, 64)
			if err != nil || end < start {
				return ByteRange{}, false, fault.New(fault.CodeRangeInvalid, false, nil)
			}
			if end >= size {
				end = size - 1
			}
		}
	}
	return ByteRange{Start: start, End: end}, true, nil
}
