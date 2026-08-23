package catalog

import (
	"cmp"
	"strings"
)

func NewOrderKey(metadata Metadata, path string, id TrackID) OrderKey {
	return OrderKey{
		Disc:        OrderedNumber{Known: metadata.Disc > 0, Value: metadata.Disc},
		Track:       OrderedNumber{Known: metadata.Track > 0, Value: metadata.Track},
		NaturalPath: normalizePath(path),
		TrackID:     id,
	}
}

func CompareTrackOrder(left, right Track) int {
	if value := compareOrderedNumber(left.Order.Disc, right.Order.Disc); value != 0 {
		return value
	}
	if value := compareOrderedNumber(left.Order.Track, right.Order.Track); value != 0 {
		return value
	}
	if value := naturalCompare(left.Order.NaturalPath, right.Order.NaturalPath); value != 0 {
		return value
	}
	return cmp.Compare(left.TrackID, right.TrackID)
}

func compareOrderedNumber(left, right OrderedNumber) int {
	if left.Known != right.Known {
		if left.Known {
			return -1
		}
		return 1
	}
	if !left.Known {
		return 0
	}
	return cmp.Compare(left.Value, right.Value)
}

func naturalCompare(left, right string) int {
	li, ri := 0, 0
	for li < len(left) && ri < len(right) {
		if isDigit(left[li]) && isDigit(right[ri]) {
			le, re := li, ri
			for le < len(left) && isDigit(left[le]) {
				le++
			}
			for re < len(right) && isDigit(right[re]) {
				re++
			}
			ln, rn := strings.TrimLeft(left[li:le], "0"), strings.TrimLeft(right[ri:re], "0")
			if ln == "" {
				ln = "0"
			}
			if rn == "" {
				rn = "0"
			}
			if len(ln) != len(rn) {
				return cmp.Compare(len(ln), len(rn))
			}
			if value := cmp.Compare(ln, rn); value != 0 {
				return value
			}
			li, ri = le, re
			continue
		}
		if left[li] != right[ri] {
			return cmp.Compare(left[li], right[ri])
		}
		li++
		ri++
	}
	return cmp.Compare(len(left)-li, len(right)-ri)
}
func isDigit(value byte) bool          { return value >= '0' && value <= '9' }
func filepathSlash(path string) string { return strings.ReplaceAll(path, "\\", "/") }
